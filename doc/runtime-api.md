# Runtime API

The runtime exposes health, status, generation, and command execution endpoints on the configured port.

## Endpoints

- `GET /healthz` returns the runtime health payload, including the runtime config path and workspace root.
- `GET /status` returns active runtime work such as in-flight generation or command execution.
- `POST /v1/generate` keeps the existing Bedrock-backed text generation path.
- `POST /v1/execute` runs a command in the runtime workspace context and returns `stdout`, `stderr`, and `exit_code`.

## Example Request

```bash
curl -fsS -X POST http://127.0.0.1:8080/v1/execute \
  -H 'Content-Type: application/json' \
  -d '{"command":"pwd"}'
```

## Workspace Rules

The default execution workspace is `/opt/agenthub/workspace`.
If sandbox filesystem allow-lists are configured, requested working directories must stay inside one of those allowed roots.

## Related Commands

- `agenthub redeploy --config agenthub.yaml`
- `agenthub status`

If you need to provision the host before hitting the runtime, see [CLI Usage Guide](./cli-usage.md) or [Manual Bootstrap Commands](./manual-bootstrap-commands.md).
