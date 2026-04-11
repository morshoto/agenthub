# Contributing to agenthub

Thank you for your interest in contributing to agenthub!

## Getting Started

1. Fork the repository
2. Clone your fork: `git clone https://github.com/morshoto/agenthub.git`
3. Create a feature branch: `git checkout -b feat/your-feature`
4. Make your changes
5. Run tests: `make test`
6. Run formatting and validation checks: `make validate`
7. Commit your changes
8. Push to your fork and submit a pull request

## Development Setup

```bash
# Verify toolchain
go version
# Install dependencies
go mod download
# Build
make build
# Run tests
make test
# Run validation
make validate
# Run deeper validation
make validate-deep
# Run race tests
make test-race
````

## Project Structure

```text
agenthub/
├── cmd/agenthub/              # Canonical CLI entrypoint
├── internal/
│   ├── app/                   # Cobra commands and top-level app behavior
│   ├── config/                # Configuration loading and validation
│   ├── runtime/               # Runtime API logic
│   ├── runtimeinstall/        # Host install logic
│   ├── provider/              # Provider abstractions
│   │   └── aws/               # AWS-specific behavior
│   ├── setup/                 # Interactive setup flows
│   └── slackbot/              # Slack deployment and integration logic
├── infra/                     # Infrastructure assets grouped by provider
├── agents/                    # Checked-in agent definitions and examples
└── doc/                       # Project documentation and supporting material
```

## Development Workflow

Use the fast validation loop while iterating:

```bash
make validate
```

For changes that touch concurrency-sensitive code or need stronger verification, also run:

```bash
make test-race
make validate-deep
```

## Adding or Changing Code

1. Keep new code close to the domain it belongs to
2. Avoid creating new top-level packages unless the existing layout is clearly insufficient
3. Separate pure logic from cloud or host side effects where possible
4. Prefer testable helpers over embedding logic directly in command handlers
5. Add or update tests for behavior changes
6. Update `README.md` when changes affect setup, deployment, or runtime API usage

## Code Style

* Follow [Effective Go](https://go.dev/doc/effective_go) guidelines
* Use `gofmt` for formatting
* Keep `gofmt -l .` clean
* Keep `go vet ./...` clean
* Add or update tests for behavior changes
* Prefer small, focused changes over wide refactors mixed with feature work

## Testing

Tests should live alongside the packages they exercise as `*_test.go` files. When changing behavior, update the nearest existing test file or add a new package-local test.

```bash
# Unit Tests
make test
# Validation
make validate
# Race Tests
make test-race
# Deep Validation
make validate-deep
```

## Branch Naming

We commonly use the following branch prefixes. This is not a hard requirement, but following it helps repository automation apply labels consistently:

| Branch Name | Description        | Supplemental |
| ----------- | ------------------ | ------------ |
| main        | latest release     | CD action    |
| feat/*      | development branch | CI action    |
| refactor/*  | refactor branch    | CI action    |
| fix/*       | hotfix branch      | CI action    |
| hotfix/*    | hotfix branch      | CI action    |

## Pull Request Guidelines

1. **One PR per feature/fix** - Keep changes focused
2. **Write tests** - Add or update tests for behavior changes
3. **Update documentation** - Update `README.md` for user-facing setup, deployment, or runtime changes
4. **Pass CI** - Formatting, vet, build, and tests must pass
5. **Reference issues** - Link related issues with `Closes: #123`
6. **Use the PR template** - Follow `.github/PULL_REQUEST_TEMPLATE.md`
7. **Open draft PRs when needed** - Use draft PRs for incomplete work

## Issues

GitHub issue templates are available for bugs, features, improvements, design proposals, and questions. Prefer using those templates when opening new issues.

## Release Notes

Release publishing is handled by GitHub Actions. Pushes to `main` update the `tagpr` release PR in `.github/workflows/tagpr.yml`, and merging that PR creates the semver tag that triggers `.github/workflows/publish.yml`. The `tagpr` job uses a GitHub App token minted from `TAGPR_APP_ID` and `TAGPR_APP_PRIVATE_KEY`, so release automation does not depend on a personal access token. Contributors do not need to build release artifacts manually for normal PRs, but user-visible changes should be described clearly in commits and pull requests because release notes are generated from git history.
