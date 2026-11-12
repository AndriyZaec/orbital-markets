# syntax=docker/dockerfile:1.7
#
# Build context is `apps/api` (not the repo root) so the local .dockerignore
# applies. Deploy with: `cd apps/api && flyctl deploy --remote-only`.

# musl produces a static helper for the distroless/static runtime. Keep this
# toolchain aligned with phoenix-helper/rust-toolchain.toml.
FROM rust:1.89.0-alpine3.22@sha256:4b800f2e72e04be908e5f634c504c741bd943b763d1d8ad7b096cc340e1b5b46 AS phoenix-builder
ARG TARGETARCH
WORKDIR /src/phoenix-helper
# Host proc-macro linking needs the musl startup objects as well as rustc.
RUN apk add --no-cache musl-dev
COPY phoenix-helper/Cargo.toml phoenix-helper/Cargo.lock phoenix-helper/rust-toolchain.toml ./
COPY phoenix-helper/src ./src
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,id=phoenix-target-${TARGETARCH},target=/src/phoenix-helper/target \
    cargo build --release --locked --bin phoenix-helper \
    && mkdir -p /out \
    && cp target/release/phoenix-helper /out/phoenix-helper

FROM golang:1.25-alpine AS builder
ARG TARGETARCH
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# No CGO needed: modernc.org/sqlite is pure Go.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# distroless/static (root variant) — Fly mounts volumes as root:root so a
# nonroot container cannot create files under /data without an init step.
FROM gcr.io/distroless/static-debian12
WORKDIR /
COPY --from=builder /out/server /server
COPY --from=phoenix-builder /out/phoenix-helper /phoenix-helper
EXPOSE 8080
ENTRYPOINT ["/server"]
