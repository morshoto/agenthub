
```bash
# Regenerating the module sums
go mod tidy
# Run the interactive setup
go run ./cmd/openclaw init
# Create instances with config files
go run ./cmd/openclaw create
# Deploy to slack channel with .env data
go run ./cmd/openclaw slack deploy
```

```bash
# Run tests
go test ./... -v
```

## Release Flow

- `publish` is handled by `.github/workflows/publish.yml`
- `release` is handled by `.github/workflows/release.yml`
- pushing a `v*` tag creates a draft release with `openclaw_<version>_darwin_arm64` and `openclaw_<version>_linux_amd64`
- the release workflow promotes the draft after running smoke tests
