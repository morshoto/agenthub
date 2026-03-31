package terraform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerraformBackendPassesAWSSettingsToTerraform(t *testing.T) {
	dir := t.TempDir()

	moduleDir := filepath.Join(dir, "module")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(module) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(moduleDir, "main.tf"), []byte("terraform {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(main.tf) error = %v", err)
	}

	workdir := filepath.Join(dir, "work")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("MkdirAll(work) error = %v", err)
	}
	varsPath := filepath.Join(workdir, "vars.json")
	if err := os.WriteFile(varsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(vars.json) error = %v", err)
	}

	envFile := filepath.Join(dir, "terraform-env.txt")
	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(bin) error = %v", err)
	}
	script := fmt.Sprintf(`#!/bin/sh
set -eu
{
  echo "AWS_PROFILE=${AWS_PROFILE:-}"
  echo "AWS_SDK_LOAD_CONFIG=${AWS_SDK_LOAD_CONFIG:-}"
  echo "AWS_REGION=${AWS_REGION:-}"
  echo "AWS_DEFAULT_REGION=${AWS_DEFAULT_REGION:-}"
  echo "ARGS=$*"
} > %q
exit 0
`, envFile)
	terraformPath := filepath.Join(binDir, "terraform")
	if err := os.WriteFile(terraformPath, []byte(script), 0o700); err != nil {
		t.Fatalf("WriteFile(terraform) error = %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath)

	backend := &TerraformBackend{
		Binary:    "terraform",
		ModuleDir: moduleDir,
		Profile:   "test-profile",
		Region:    "us-east-1",
	}

	if err := backend.Init(context.Background(), workdir); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if err := backend.Plan(context.Background(), workdir, varsPath); err != nil {
		t.Fatalf("Plan() error = %v", err)
	}

	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatalf("ReadFile(envFile) error = %v", err)
	}
	got := string(data)
	for _, fragment := range []string{
		"AWS_PROFILE=test-profile",
		"AWS_SDK_LOAD_CONFIG=1",
		"AWS_REGION=us-east-1",
		"AWS_DEFAULT_REGION=us-east-1",
		"ARGS=plan -input=false -no-color -var-file=",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("env output = %q, want %q", got, fragment)
		}
	}
}
