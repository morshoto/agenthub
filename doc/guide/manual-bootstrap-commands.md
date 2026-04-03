# Manual Bootstrap Commands

This guide lists the commands you can run before relying on the Go CLI for automation.
It is written for the AWS path used by this repository.

## 1. Sign in to AWS

Use your AWS profile first:

```bash
aws sso login --profile sso-dev
aws sts get-caller-identity --profile sso-dev
```

If the identity call succeeds, your AWS credentials are ready.

## 2. Generate Terraform variables from YAML

The Terraform module lives in `infra/aws/ec2`.

Generate a `terraform.tfvars` file from your OpenClaw config:

```bash
openclaw infra tfvars --config openclaw.yaml --output infra/aws/ec2/terraform.tfvars
```

If you want to pin the AWS profile explicitly, pass `--profile sso-dev`.
If you omit it and run interactively, the CLI will prompt you to choose a profile or type one in.

This command reads the YAML config, resolves the SSH public key, and writes Terraform-compatible `terraform.tfvars` variables.
If your config only has `image.name`, Terraform resolves the AMI from AWS during `plan` or `apply`.
The generated file also includes `aws_profile`, so Terraform uses the same AWS profile you selected in `openclaw`.

## 3. Create the Terraform infrastructure

```bash
terraform -chdir=infra/aws/ec2 init
terraform -chdir=infra/aws/ec2 plan -var-file=terraform.tfvars
terraform -chdir=infra/aws/ec2 apply -var-file=terraform.tfvars
```

### Recreate from scratch

If you want to tear everything down and rebuild it:

```bash
terraform -chdir=infra/aws/ec2 destroy -var-file=terraform.tfvars
```

## 4. Connect to the instance

After Terraform finishes, use the printed connection info or the EC2 public IP.

```bash
ssh -i ~/.ssh/id_ed25519 ubuntu@<public-ip>
```

If the instance is private, connect from a bastion or SSM session instead.

## 5. Install Docker on the host

The runtime checks expect Docker to be present.

```bash
sudo apt-get update
sudo apt-get install -y docker.io
sudo systemctl enable --now docker
sudo usermod -aG docker ubuntu
newgrp docker
docker info
```

If you are using a GPU instance, you can also check the NVIDIA driver path:

```bash
nvidia-smi -L
```

## 6. Authenticate Codex locally

If you want to use the Codex CLI on your workstation, run:

```bash
codex --login
```

This opens the browser-based sign-in flow and stores the local Codex credential cache.

If you need to troubleshoot the OAuth flow, see:

- https://note.com/akira_papa_ai/n/ne3a82fe5205f
- https://zenn.dev/aria3/articles/openclaw-oauth-troubleshooting

## 7. Verify the host

Once Docker is installed and the runtime is ready, verify the machine:

```bash
docker info
```

For a GPU host, also check:

```bash
nvidia-smi
```

## Notes

- This is the manual path. The Go CLI automates most of these steps.
- The Terraform commands above can work with `image.name` only, but `plan` and `apply` still need AWS access to resolve the AMI.
- If you regenerate `terraform.tfvars` with a different AWS profile, the `aws_profile` value in the file changes too.
- If you are rebuilding often, keep the generated `terraform.tfvars` file around and regenerate it only when the YAML changes.
