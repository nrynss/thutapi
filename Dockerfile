# Thutapi — T1 deployment path.
#
# Multi-stage build: golang compiles the binary, distroless ships it.
# Matches the Mosaic shape minus the Node stage (project.md §Packaging).
#
# Build:   docker build -t thutapi:local .
# Run:     docker run --rm -p 8080:8080 -e PORT=8080 thutapi:local
# Smoke:   deploy/docker-run.sh (Hetzner-shape, Traefik labels)
#
# Notes:
#   - CGO_ENABLED=0 keeps modernc.org/sqlite pure-Go (T3) and distroless viable.
#   - -trimpath strips the local build path from the binary; -ldflags="-s -w"
#     strips symbol/debug tables. Together they minimise image size.
#   - Distroless nonroot image has no shell, no apt, no busybox. /healthz is
#     served over plain HTTP on 0.0.0.0:${PORT} (default 8080).
#
# Pin from project.md §Packaging and AGENTS.md §Stack and ground rules.

# --- builder ---------------------------------------------------------------
FROM golang:1.27.1-bookworm AS builder

WORKDIR /src

# Module download. Kept in its own layer so a code edit does not re-fetch
# the (small) dependency set.
COPY go.mod go.sum* ./
RUN go mod download

# Rest of the source.
COPY . .

# Static, stripped binary at /out/thutapi. The build context is the repo root,
# so the package path is ./cmd/thutapi (matches cmd/thutapi/main.go's package
# declaration `package main`).
#
# -trimpath: strip local paths from the binary so the image is reproducible
#            across checkouts and doesn't leak the build host's filesystem.
# -ldflags="-s -w": drop the symbol table and DWARF debug info.
# CGO_ENABLED=0:    pure-Go binary; required by distroless (no glibc).
#
# VERSION is injected by the Hetzner deploy script via --build-arg if desired;
# default "dev" keeps a clean `docker build .` working.
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/thutapi ./cmd/thutapi

# --- runtime ---------------------------------------------------------------
FROM gcr.io/distroless/base-debian12:nonroot

# /healthz is on 0.0.0.0:${PORT}. The distroless nonroot image runs as uid
# 65532 (user "nonroot"). Traefik on the box talks to it on the published
# port.
#
# PORT defaults to 8080 to match project.md §Packaging and the Traefik label
# traefik.http.services.thutapi.loadbalancer.server.port=8080.
ENV PORT=8080

# Media, generated content and SQLite live on a Docker volume mounted at
# /data. See deploy/README.md for the operator-facing layout.
VOLUME ["/data"]

# Healthcheck is intentionally omitted: distroless has no curl/wget. The
# Hetzner Traefik orchestrator probes /healthz itself; T1's smoke verification
# is `curl 127.0.0.1:18080/healthz` from the workstation.

EXPOSE 8080

COPY --from=builder /out/thutapi /thutapi

USER nonroot:nonroot

ENTRYPOINT ["/thutapi"]
