GO ?= go

.PHONY: build changelog fmt-check vet test test-race validate validate-deep

build:
	$(GO) build ./cmd/agenthub

changelog:
	python3 .github/scripts/render_changelog.py \
		--repo-url https://github.com/morshoto/agenthub \
		--full-changelog-out doc/changelog.md

fmt-check:
	test -z "$$(gofmt -l .)"

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

validate: build fmt-check vet test

validate-deep: validate test-race
