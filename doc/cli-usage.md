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

Show merged agent config status under `agents/`:

```bash
agenthub status
```

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
