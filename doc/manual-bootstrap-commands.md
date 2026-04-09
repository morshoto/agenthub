# Manual Bootstrap Commands

This guide lists the commands you can run before relying on the Go CLI for automation.
It is written for the AWS path used by this repository.

## 1. Sign in to AWS

Use your AWS profile first. Interactive `agenthub` commands can now launch this same browser login automatically when they detect that your selected profile has no usable credentials:

```bash
aws sso login --profile sso-dev
aws sts get-caller-identity --profile sso-dev
```

If the identity call succeeds, your AWS credentials are ready.

## 2. Generate Terraform variables from YAML

The Terraform module lives in `infra/aws/ec2`.

Generate a `terraform.tfvars` file from your AgentHub config:

```bash
agenthub infra tfvars --config agenthub.yaml --output infra/aws/ec2/terraform.tfvars
```

If you want to pin the AWS profile explicitly, pass `--profile sso-dev`.
If you omit it and run interactively, the CLI will prompt you to choose a profile or type one in, and it can open the AWS SSO browser flow to refresh credentials if the selected profile needs it.

This command reads the YAML config, resolves the SSH public key, stages the current working tree as a bootstrap archive, and writes Terraform-compatible `terraform.tfvars` variables.
If GitHub App auth is configured, it carries the project-owned Secrets Manager ARN into Terraform so the EC2 instance role can read the private key secret at runtime.
The generated file includes deploy-time values such as `aws_profile`, `runtime_port`, `runtime_cidr`, and `source_archive_url`, so Terraform can create the EC2 instance and leave runtime installation to the SSH-based `install` stage.
Treat it as a deploy helper rather than a pure formatter: it depends on a usable SSH private key path, a resolvable AWS profile, the current git worktree state, and a GitHub App secret ARN if you want the host to clone private repositories.

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

## 5. Wait for bootstrap

The EC2 user-data script prepares the host, writes the runtime config, and marks bootstrap complete.
You can watch the bootstrap log over SSH if you want to inspect what happened:

```bash
sudo tail -f /var/log/agenthub-bootstrap.log
```

When bootstrap completes, the marker file appears:

```bash
test -f /opt/agenthub/bootstrap.done
```

## 6. Authenticate Codex locally

If you want to use the Codex CLI on your workstation, run:

```bash
agenthub onboard --auth-choice openai-codex
```

This opens the browser-based sign-in flow and stores the local Codex credential cache.
If you prefer to invoke the CLI directly, `codex --login` is equivalent.
You do not need to provide an OpenAI API key for this path.

If you need to troubleshoot the OAuth flow, see:

- https://note.com/akira_papa_ai/n/ne3a82fe5205f
- https://zenn.dev/aria3/articles/agenthub-oauth-troubleshooting

## 7. Verify the host

Once bootstrap is ready, verify the machine:

```bash
docker info
docker ps --filter name='^/agenthub$'
curl -fsS http://127.0.0.1:8080/healthz
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
- `runtime_cidr` defaults to `0.0.0.0/0`, which keeps the runtime health endpoint publicly reachable for external verification.
