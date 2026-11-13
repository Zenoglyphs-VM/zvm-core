# syntax=docker/dockerfile:1.7
#
# zvm: the CLI, which also runs the devnet faucet (`zvm faucet`).
#
#   docker build -t zvm .

FROM golang:1.26.11-alpine3.24@sha256:ae52b5bc6ed045a9ed26ab03dc19327a802a9cc9aa8b27ed57f5c6b60579b0bb AS build
ENV GOTOOLCHAIN=local GOWORK=off GOTELEMETRY=off GOFLAGS=-mod=readonly
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    test "$(go env GOVERSION)" = go1.26.11 && \
    go mod download && \
    go mod verify
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/zvm ./cmd/zvm

FROM alpine:3.22
RUN apk add --no-cache ca-certificates wget
COPY --from=build /out/zvm /usr/local/bin/zvm
ENTRYPOINT ["zvm"]
