GO ?= go

.PHONY: build fmt-check vet test validate

build:
	$(GO) build ./cmd/agenthub

fmt-check:
	test -z "$$(gofmt -l .)"

vet:
	$(GO) vet ./...

test:
	$(GO) test ./...

validate: build fmt-check vet test
