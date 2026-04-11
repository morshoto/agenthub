# CLI Usage Guide

This page groups the common `agenthub` workflows by task instead of listing commands without context.

## Setup and Provisioning

Run the interactive setup:

```bash
agenthub init
```

Create infrastructure from a config file:

```bash
agenthub create --config agenthub.yaml
```

Notes:

- `agenthub init` and `agenthub create` both require a usable AWS profile.
- Pass `--profile` or set `AWS_PROFILE` before running them.
- If only one AWS profile is discovered locally, `agenthub` auto-selects it instead of prompting.
- `agenthub init` writes public networking so the generated config is ready for `agenthub create`.

## Runtime Operations

Re-apply runtime deployment to an existing host:

```bash
agenthub redeploy --config agenthub.yaml
```

Preview the managed file changes a redeploy would make without mutating the host:

```bash
agenthub redeploy --config agenthub.yaml --dry-run
```

`agenthub redeploy --dry-run` resolves the target over SSH, compares the generated runtime config, systemd unit, and provider environment file against the current host state, and reports that the runtime binary would be replaced. It does not upload files, restart services, or run post-apply verification.

Start the runtime service for one deployed agent:

```bash
agenthub runtime start --config agenthub.yaml
```

Restart the runtime service for one deployed agent:

```bash
agenthub runtime restart --config agenthub.yaml
```

Stop the runtime service for one deployed agent:

```bash
agenthub runtime stop --config agenthub.yaml
```

Show merged agent config status under `agents/`:

```bash
agenthub status
```

Show the same status as structured JSON for automation:

```bash
agenthub status --output json
```

Preview config edits without writing the config file:

```bash
agenthub config update --config agenthub.yaml --dry-run --set runtime.model=gpt-5.4
```

Update one agent's integration credentials without editing files manually:

```bash
agenthub config secret update --config agenthub.yaml --set slack.bot_token=xoxb-... --set slack.app_token=xapp-...
```

Inspect one deployed agent in detail:

```bash
agenthub inspect alpha --ssh-key ~/.ssh/id_ed25519
```

Show the same inspection report as structured JSON for automation:

```bash
agenthub inspect alpha --ssh-key ~/.ssh/id_ed25519 --output json
```

`agenthub inspect` reads the merged local config for the selected agent, resolves the recorded deployment target, and probes the remote runtime state over SSH.

## Infrastructure Teardown

Destroy infrastructure for one deployed agent:

```bash
agenthub infra destroy --config agenthub.yaml
```

This is the supported teardown path for one deployed agent. It preserves the local config and clears stale deployment state after a successful destroy.

## Slack Integration

Deploy the Slack integration:

```bash
agenthub slack deploy --config agenthub.yaml
```

`agenthub slack deploy` uses `infra.instance_id` from the config created by `agenthub create`. Pass `--target` if you want to override it.

## Version

Print the release version:

```bash
agenthub --version
```

## Related Guides

- Use [Runtime API](./runtime-api.md) if you need the HTTP endpoints exposed by the runtime.
- Use [Manual Bootstrap Commands](./manual-bootstrap-commands.md) if you want to step through the AWS path manually.
