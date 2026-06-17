# syntax=docker/dockerfile:1.7
#
# Build context is `apps/api` (not the repo root) so the local .dockerignore
# applies. Deploy with: `cd apps/api && flyctl deploy --remote-only`.

FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

COPY . .

# No CGO needed: modernc.org/sqlite is pure Go.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# distroless/static (root variant) — Fly mounts volumes as root:root so a
# nonroot container cannot create files under /data without an init step.
FROM gcr.io/distroless/static-debian12
WORKDIR /
COPY --from=builder /out/server /server
EXPOSE 8080
ENTRYPOINT ["/server"]
