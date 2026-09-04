#!/usr/bin/env bash
#
# Thutapi — Hetzner `docker run` for the foleyflow box.
#
# Brings up the thutapi container behind the existing Traefik v3 instance so
# it serves https://thutapi.nryn.dev. TLS, cert renewal and routing are
# handled by the existing letsencrypt certresolver — the five labels below
# are the only contract between this container and Traefik.
#
# Usage (on the box):
#
#   export GMI_API_KEY=...               # required: GMI Cloud inference key
#   ./deploy/docker-run.sh               # default image, default host port
#   IMAGE=ghcr.io/.../thutapi:abc123 ./deploy/docker-run.sh
#   NAME=thutapi-canary ./deploy/docker-run.sh
#
# To stop:
#
#   docker rm -f thutapi
#
# This script does NOT pull secrets from anywhere. GMI_API_KEY must be set
# in the operator's shell environment. The repo is public for the whole
# judging period, so it must never appear in this file or in image layers.
#
# Refs:
#   dev-diary/project.md §Deployment — the box already does this
#   dev-diary/PLAN.md §T1 — The deployment path
#   AGENTS.md §Safety and data rules

set -euo pipefail

IMAGE="${IMAGE:-thutapi:local}"
NAME="${NAME:-thutapi}"
DATA_DIR="${DATA_DIR:-/srv/thutapi/data}"
HOST_PORT="${HOST_PORT:-}"

# GMI_API_KEY is the only required secret. Fail loudly if it is missing so
# the container cannot silently start with an unset inference key.
if [[ -z "${GMI_API_KEY:-}" ]]; then
  echo "error: GMI_API_KEY is not set. Export it before running this script." >&2
  echo "       The repo is public for the judging period; it must never" >&2
  echo "       be hard-coded into this file or any image layer." >&2
  exit 1
fi

# Generate an unguessable token for the voice-sample upload path (T13).
# The Hetzner box exposes /upload for one-shot voice captures; GMI's
# Speech 2.8 source_audio fetches the resulting URL. Short-lived and
# unguessable keeps a child's voice off any directory listing.
UPLOAD_TOKEN="${UPLOAD_TOKEN:-$(openssl rand -hex 16)}"
mkdir -p "${DATA_DIR}"
printf '%s' "${UPLOAD_TOKEN}" > "${DATA_DIR}/upload-token"
chmod 0600 "${DATA_DIR}/upload-token"


# Build the docker run command. -e flags pass through to the binary's
# parseConfig (cmd/thutapi/main.go: parseConfig reads environment only).
#
# Port mapping: only emit -p if HOST_PORT is set. On foleyflow the container
# listens on 8080 internally (Traefik hits it there); the host port is
# irrelevant because Traefik routes by labels, not published ports.
DOCKER_ARGS=(
  --detach
  --restart unless-stopped
  --name "${NAME}"
  --hostname "${NAME}"

  # Persistence: SQLite + generated media live on a host bind mount. The
  # container expects DATA_DIR and writes under it.
  --mount "type=bind,source=${DATA_DIR},target=/data"

  # Runtime configuration. PORT is what the binary listens on; loadbalancer
  # below points Traefik at the same port.
  --env "PORT=8080"
  --env "DATA_DIR=/data"
  --env "GMI_API_KEY=${GMI_API_KEY}"
  --env "UPLOAD_TOKEN=${UPLOAD_TOKEN}"

  # --- Traefik v3 labels ---
  # traefik.enable: opt this container into routing.
  # router:        entrypoint=websecure (443), Host header exact match, letsencrypt.
  # service:       backend port 8080 (matches PORT above and Dockerfile EXPOSE).
  --label "traefik.enable=true"
  --label "traefik.http.routers.thutapi.entrypoints=websecure"
  --label "traefik.http.routers.thutapi.rule=Host(\`thutapi.nryn.dev\`)"
  --label "traefik.http.routers.thutapi.tls.certresolver=letsencrypt"
  --label "traefik.http.services.thutapi.loadbalancer.server.port=8080"
)

if [[ -n "${HOST_PORT}" ]]; then
  DOCKER_ARGS+=(--publish "${HOST_PORT}:8080")
fi

echo "Starting ${NAME} from ${IMAGE}..."
docker run "${DOCKER_ARGS[@]}" "${IMAGE}"

echo
echo "Container started. Useful follow-ups:"
echo "  docker logs -f ${NAME}"
echo "  curl -fsS https://thutapi.nryn.dev/healthz"
echo "  curl -fsS http://127.0.0.1:8080/healthz     # if HOST_PORT=8080"
echo "Voice-sample bearer (UPLOAD_TOKEN) — see ${DATA_DIR}/upload-token for the token; not echoed to stdout."

