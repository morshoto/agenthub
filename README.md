<p align="center">
  <img src="https://img.shields.io/badge/Go-1.25.0-00ADD8?logo=go&logoColor=white" alt="Go 1.25.0" />
  <a href="https://github.com/morshoto/agenthub/releases"><img src="https://img.shields.io/github/downloads/morshoto/agenthub/total?label=downloads&logo=github" alt="GitHub release downloads total" /></a>
  <img src="https://github.com/morshoto/agenthub/actions/workflows/go-ci.yml/badge.svg?branch=main" alt="Go CI" />
  <a href="https://deepwiki.com/morshoto/agenthub"><img src="https://img.shields.io/badge/DeepWiki-morshoto%2Fagenthub-blue.svg?logo=data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAACwAAAAyCAYAAAAnWDnqAAAAAXNSR0IArs4c6QAAA05JREFUaEPtmUtyEzEQhtWTQyQLHNak2AB7ZnyXZMEjXMGeK/AIi+QuHrMnbChYY7MIh8g01fJoopFb0uhhEqqcbWTp06/uv1saEDv4O3n3dV60RfP947Mm9/SQc0ICFQgzfc4CYZoTPAswgSJCCUJUnAAoRHOAUOcATwbmVLWdGoH//PB8mnKqScAhsD0kYP3j/Yt5LPQe2KvcXmGvRHcDnpxfL2zOYJ1mFwrryWTz0advv1Ut4CJgf5uhDuDj5eUcAUoahrdY/56ebRWeraTjMt/00Sh3UDtjgHtQNHwcRGOC98BJEAEymycmYcWwOprTgcB6VZ5JK5TAJ+fXGLBm3FDAmn6oPPjR4rKCAoJCal2eAiQp2x0vxTPB3ALO2CRkwmDy5WohzBDwSEFKRwPbknEggCPB/imwrycgxX2NzoMCHhPkDwqYMr9tRcP5qNrMZHkVnOjRMWwLCcr8ohBVb1OMjxLwGCvjTikrsBOiA6fNyCrm8V1rP93iVPpwaE+gO0SsWmPiXB+jikdf6SizrT5qKasx5j8ABbHpFTx+vFXp9EnYQmLx02h1QTTrl6eDqxLnGjporxl3NL3agEvXdT0WmEost648sQOYAeJS9Q7bfUVoMGnjo4AZdUMQku50McDcMWcBPvr0SzbTAFDfvJqwLzgxwATnCgnp4wDl6Aa+Ax283gghmj+vj7feE2KBBRMW3FzOpLOADl0Isb5587h/U4gGvkt5v60Z1VLG8BhYjbzRwyQZemwAd6cCR5/XFWLYZRIMpX39AR0tjaGGiGzLVyhse5C9RKC6ai42ppWPKiBagOvaYk8lO7DajerabOZP46Lby5wKjw1HCRx7p9sVMOWGzb/vA1hwiWc6jm3MvQDTogQkiqIhJV0nBQBTU+3okKCFDy9WwferkHjtxib7t3xIUQtHxnIwtx4mpg26/HfwVNVDb4oI9RHmx5WGelRVlrtiw43zboCLaxv46AZeB3IlTkwouebTr1y2NjSpHz68WNFjHvupy3q8TFn3Hos2IAk4Ju5dCo8B3wP7VPr/FGaKiG+T+v+TQqIrOqMTL1VdWV1DdmcbO8KXBz6esmYWYKPwDL5b5FA1a0hwapHiom0r/cKaoqr+27/XcrS5UwSMbQAAAABJRU5ErkJggg==" alt="DeepWiki"></a>
  <img src="https://img.shields.io/github/license/morshoto/agenthub" alt="License" />
</p>

`agenthub` is a Go CLI for provisioning and operating multi-agents environments on cloud instance, installing the runtime on a host, and wiring Slack integrations.

### Install

**Homebrew**: install from the `morshoto/agenthub` tap <br>
**Nix**: install directly from this repository using a flake <br>
**GitHub Releases**: download the binary that matches your platform from the latest release and place it on your `PATH`

```bash
# Install with Homebrew
brew tap morshoto/agenthub && brew install agenthub
# Install directly from this repository
nix profile install github:morshoto/agenthub

# Run it without installing
nix run github:morshoto/agenthub -- version

# macOS arm64 binary from the latest release
gh release download latest --pattern 'agenthub_*_darwin_arm64'

# Linux amd64 binary from the latest release
gh release download latest --pattern 'agenthub_*_linux_amd64'
```

Release binaries remain the source of truth for manual downloads.
The Homebrew tap is published separately at `https://github.com/morshoto/homebrew-agenthub`.

### Supported OS / arch

- Linux `amd64`
- macOS `arm64`
- Nix on the same systems supported by nixpkgs
- Homebrew on the same systems supported by the tap

### Common Commands

```bash
# Run the interactive setup
agenthub init
# Create instances from a config file
agenthub create --config agenthub.yaml
# Deploy Slack integration
agenthub slack deploy --config agenthub.yaml
# Show merged agent config status under `agents/`
agenthub status
# Print the release version
agenthub --version
```

Notes:

- `agenthub init` and `agenthub create` both require a usable AWS profile. Pass `--profile` or set `AWS_PROFILE` before running them.
- If only one AWS profile is discovered locally, `agenthub` will auto-select it instead of asking.
- `agenthub init` now always writes public networking so the generated config is ready for `agenthub create`.
- `agenthub slack deploy` uses `infra.instance_id` from the config created by `agenthub create`; pass `--target` if you want to override it.

### Local Validation

```bash
# Run the standard local validation flow
make validate

# Run individual checks
make build
make fmt-check
make vet
make test
```

The canonical build entrypoint is `./cmd/agenthub`. Local builds, CI, packaging, and runtime install flows all build from that path.

### Publishing

- Release builds are created by `.github/workflows/publish.yml`.
- The workflow also updates the `morshoto/homebrew-agenthub` tap.
- Set the `HOMEBREW_TAP_TOKEN` repository secret on `morshoto/agenthub` so the tap push step can authenticate.
- `go-ci` runs a dry-run publish check against a temporary Homebrew tap before release jobs run.
