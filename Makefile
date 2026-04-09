GO ?= go

.PHONY: build fmt-check vet test test-race validate validate-deep

build:
	$(GO) build ./cmd/agenthub

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
