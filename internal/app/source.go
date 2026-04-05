package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var resolveSourceArchiveURLFunc = resolveSourceArchiveURL

func resolveSourceArchiveURL(ctx context.Context, profile, region string) (string, string, error) {
	remoteURL, err := gitOutput(ctx, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", "", err
	}
	remoteURL = strings.TrimSpace(remoteURL)
	if remoteURL == "" {
		return "", "", errors.New("git remote origin is required to bootstrap the Docker image")
	}

	ref, err := gitOutput(ctx, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", "", errors.New("git HEAD revision is required to bootstrap the Docker image")
	}

	archivePath, err := archiveWorkingTree(ctx, ref)
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(filepath.Dir(archivePath))

	accountID, err := awsOutput(ctx, profile, region, "sts", "get-caller-identity", "--query", "Account", "--output", "text")
	if err != nil {
		return "", "", err
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return "", "", errors.New("aws account id is required to stage the Docker bootstrap archive")
	}

	bucketName := fmt.Sprintf("agenthub-bootstrap-%s-%s", accountID, region)
	if _, err := awsOutput(ctx, profile, region, "s3api", "head-bucket", "--bucket", bucketName); err != nil {
		if err := createBootstrapBucket(ctx, profile, region, bucketName); err != nil {
			return "", "", err
		}
	}

	objectKey := fmt.Sprintf("agenthub-%s.tar.gz", ref)
	if _, err := awsOutput(ctx, profile, region, "s3", "cp", archivePath, fmt.Sprintf("s3://%s/%s", bucketName, objectKey)); err != nil {
		return "", "", err
	}
	url, err := awsOutput(ctx, profile, region, "s3", "presign", fmt.Sprintf("s3://%s/%s", bucketName, objectKey), "--expires-in", "86400")
	if err != nil {
		return "", "", err
	}
	return strings.TrimSpace(url), ref, nil
}

func gitOutput(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

func archiveWorkingTree(ctx context.Context, ref string) (string, error) {
	tmpDir, err := os.MkdirTemp("", "agenthub-source-*")
	if err != nil {
		return "", fmt.Errorf("create temporary source archive workspace: %w", err)
	}
	archivePath := filepath.Join(tmpDir, "source.tar.gz")
	worktree, err := gitOutput(ctx, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	worktree = strings.TrimSpace(worktree)
	if worktree == "" {
		return "", errors.New("git worktree path is required to stage the Docker bootstrap archive")
	}
	cmd := exec.CommandContext(ctx, "tar",
		"--exclude=.git",
		"--exclude=.terraform",
		"--exclude=.terraform.lock.hcl",
		"--exclude=terraform.tfvars",
		"--exclude=*.pem",
		"-czf", archivePath,
		"-C", worktree,
		".",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return "", fmt.Errorf("create source archive: %s: %w", msg, err)
		}
		return "", fmt.Errorf("create source archive: %w", err)
	}
	return archivePath, nil
}

func awsOutput(ctx context.Context, profile, region string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "aws", args...)
	cmd.Env = append(os.Environ(),
		"AWS_PROFILE="+strings.TrimSpace(profile),
		"AWS_DEFAULT_REGION="+strings.TrimSpace(region),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return "", fmt.Errorf("aws %s: %s: %w", strings.Join(args, " "), msg, err)
		}
		return "", fmt.Errorf("aws %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

func createBootstrapBucket(ctx context.Context, profile, region, bucketName string) error {
	args := []string{"s3api", "create-bucket", "--bucket", bucketName}
	if strings.TrimSpace(region) != "" && region != "us-east-1" {
		args = append(args, "--create-bucket-configuration", "LocationConstraint="+region)
	}
	if _, err := awsOutput(ctx, profile, region, args...); err != nil {
		return fmt.Errorf("create bootstrap bucket %q: %w", bucketName, err)
	}
	return nil
}
