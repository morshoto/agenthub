package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAWSEC2ModuleUsesStableSSHKeyName(t *testing.T) {
	path := filepath.Join("..", "..", "infra", "aws", "ec2", "main.tf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	body := string(data)
	if !strings.Contains(body, "key_name   = trimspace(var.ssh_key_name)") {
		t.Fatalf("AWS module does not use stable ssh_key_name: %q", body)
	}
	if strings.Contains(body, `key_name   = "${trimspace(var.ssh_key_name)}-${random_id.suffix.hex}"`) {
		t.Fatalf("AWS module still appends random suffix to ssh key name: %q", body)
	}
	if !strings.Contains(body, "ignore_changes = [public_key]") {
		t.Fatalf("AWS module is missing key pair ignore_changes lifecycle: %q", body)
	}
}

func TestAWSEC2ModuleUsesStableSecurityGroupName(t *testing.T) {
	path := filepath.Join("..", "..", "infra", "aws", "ec2", "main.tf")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	body := string(data)
	if !strings.Contains(body, "name        = trimspace(var.security_group_name)") {
		t.Fatalf("AWS module does not use stable security_group_name: %q", body)
	}
	if strings.Contains(body, `name        = "${var.name_prefix}-${random_id.suffix.hex}"`) {
		t.Fatalf("AWS module still appends random suffix to security group name: %q", body)
	}
	if !strings.Contains(body, `ManagedBy   = "agenthub"`) {
		t.Fatalf("AWS module is missing security group tags for reconciliation: %q", body)
	}
}
