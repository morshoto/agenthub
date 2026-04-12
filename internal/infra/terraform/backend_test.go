package terraform

import (
	"context"
	"errors"
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

func TestTerraformBackendImportPassesAWSSettingsToTerraform(t *testing.T) {
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
	if err := backend.Import(context.Background(), workdir, "aws_key_pair.this", "demo-key"); err != nil {
		t.Fatalf("Import() error = %v", err)
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
		"ARGS=import -input=false -no-color aws_key_pair.this demo-key",
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("env output = %q, want %q", got, fragment)
		}
	}
}

func TestTerraformBackendPrepareWorkspaceRefreshesModuleAndPreservesState(t *testing.T) {
	dir := t.TempDir()

	moduleDir := filepath.Join(dir, "module")
	if err := os.MkdirAll(moduleDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(module) error = %v", err)
	}
	modulePath := filepath.Join(moduleDir, "main.tf")
	if err := os.WriteFile(modulePath, []byte("terraform {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(main.tf) error = %v", err)
	}

	workdir := filepath.Join(dir, "work")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatalf("MkdirAll(work) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "terraform.tfstate"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(terraform.tfstate) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, "stale.tf"), []byte("stale\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(stale.tf) error = %v", err)
	}
	varsPath := filepath.Join(workdir, "agenthub.auto.tfvars.json")
	if err := os.WriteFile(varsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(agenthub.auto.tfvars.json) error = %v", err)
	}

	binDir := filepath.Join(dir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(bin) error = %v", err)
	}
	terraformPath := filepath.Join(binDir, "terraform")
	if err := os.WriteFile(terraformPath, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatalf("WriteFile(terraform) error = %v", err)
	}

	oldPath := os.Getenv("PATH")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+oldPath)

	backend := &TerraformBackend{
		Binary:    "terraform",
		ModuleDir: moduleDir,
	}

	if err := backend.Init(context.Background(), workdir); err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, "terraform.tfstate")); err != nil {
		t.Fatalf("Stat(terraform.tfstate) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, "stale.tf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(stale.tf) error = %v, want not exist", err)
	}
	if _, err := os.Stat(varsPath); err != nil {
		t.Fatalf("Stat(agenthub.auto.tfvars.json) error = %v", err)
	}

	if err := os.WriteFile(modulePath, []byte("terraform {\n  required_version = \">= 1.6.0\"\n}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(main.tf updated) error = %v", err)
	}
	if err := backend.Init(context.Background(), workdir); err != nil {
		t.Fatalf("Init() second call error = %v", err)
	}
	if _, err := os.Stat(varsPath); err != nil {
		t.Fatalf("Stat(agenthub.auto.tfvars.json) after second init error = %v", err)
	}
	data, err := os.ReadFile(filepath.Join(workdir, "main.tf"))
	if err != nil {
		t.Fatalf("ReadFile(work main.tf) error = %v", err)
	}
	if !strings.Contains(string(data), "required_version") {
		t.Fatalf("work main.tf = %q, want refreshed module contents", string(data))
	}
}
