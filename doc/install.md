# Installation Guide

`agenthub` supports Homebrew, Nix, and direct release binaries.

Release binaries remain the source of truth for manual downloads.
The Homebrew tap is published separately at `https://github.com/morshoto/homebrew-agenthub`.

## Supported OS / Arch

- Linux `amd64`
- macOS `arm64`
- Nix on the same systems supported by nixpkgs
- Homebrew on the same systems supported by the tap

## Homebrew

```bash
brew tap morshoto/agenthub
brew install agenthub
```

## Nix

Install from this repository:

```bash
nix profile install github:morshoto/agenthub
```

Run without installing:

```bash
nix run github:morshoto/agenthub -- version
```

## GitHub Releases

Download the binary that matches your platform from the latest release and place it on your `PATH`.

Examples:

```bash
# macOS arm64
gh release download latest --pattern 'agenthub_*_darwin_arm64'

# Linux amd64
gh release download latest --pattern 'agenthub_*_linux_amd64'
```

## After Installation

Start the interactive setup:

```bash
agenthub init
```

If you prefer a config-driven flow, continue with [CLI Usage Guide](./cli-usage.md).
