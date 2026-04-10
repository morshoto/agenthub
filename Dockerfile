FROM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o /out/agenthub ./cmd/agenthub

FROM alpine:3.23

RUN apk add --no-cache ca-certificates curl tar

WORKDIR /opt/agenthub

# Keep the Codex CLI available in the image so Slack/Codex workflows can run
# without depending on a separate host-side install.
RUN set -eux; \
    tmpdir="$(mktemp -d)"; \
    cd "$tmpdir"; \
    curl -fsSL -o codex.tgz https://github.com/openai/codex/releases/latest/download/codex-x86_64-unknown-linux-musl.tar.gz; \
    tar -xzf codex.tgz; \
    install -m 755 codex-x86_64-unknown-linux-musl /usr/local/bin/codex; \
    rm -rf "$tmpdir"

COPY --from=build /out/agenthub /usr/local/bin/agenthub

EXPOSE 8080

ENTRYPOINT ["agenthub"]
CMD ["serve", "--listen", "0.0.0.0:8080"]
