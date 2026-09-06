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
#   ./deploy/docker-run.sh               # reads /etc/thutapi/env for secrets
#   IMAGE=ghcr.io/.../thutapi:abc123 ./deploy/docker-run.sh
#   NAME=thutapi-canary ./deploy/docker-run.sh
#   ENV_FILE=/path/to/env ./deploy/docker-run.sh
#
# To stop:
#
#   docker rm -f thutapi
#
# SECRETS. GMI_API_KEY is read from ENV_FILE (default /etc/thutapi/env),
# a root-owned 0600 file on the box. Do NOT `export GMI_API_KEY=...` in an
# interactive shell: it lands in ~/.zsh_history in plaintext and stays
# there, which is how this key actually leaks. An already-exported value
# still wins, so a one-off override works, but the file is the path the
# runbook documents.
#
# The key reaches the container through docker's NAME-ONLY --env form
# (`--env GMI_API_KEY`, no `=value`), which makes docker inherit the value
# from this script's environment. The literal secret therefore never
# appears in the process arguments, so it is not visible to `ps` on the
# host at launch.
#
# It IS still visible in `docker inspect` and in the container's
# config.v2.json on disk. That is accepted: anyone who can read those
# already has docker-group or root access to the box, and at that point
# they have the container too. Keeping it there is also what lets
# `--restart unless-stopped` bring the service back after a reboot with no
# operator present -- which matters across the judging window.
#
# The repo is public for the whole judging period, so the key must never
# appear in this file or in image layers.
#
# Refs:
#   dev-diary/project.md §Deployment — the box already does this
#   dev-diary/PLAN.md §T1 — The deployment path
#   AGENTS.md §Safety and data rules

set -euo pipefail

# Default to the image CI publishes. It is a public package, so the box
# pulls it anonymously — no docker login, no registry credential on the
# host. Prefer pinning a commit SHA over `latest` for anything you intend
# to be able to reproduce:
#   IMAGE=ghcr.io/nrynss/thutapi:1a2b3c4 ./deploy/docker-run.sh
IMAGE="${IMAGE:-ghcr.io/nrynss/thutapi:latest}"
NAME="${NAME:-thutapi}"
DATA_DIR="${DATA_DIR:-/srv/thutapi/data}"
HOST_PORT="${HOST_PORT:-}"
# Traefik's docker provider only routes to containers on this network.
NETWORK="${NETWORK:-proxy}"

# Secrets come from a root-owned file on the box, not from shell history.
# An already-set GMI_API_KEY wins, so a one-off `GMI_API_KEY=... ./docker-run.sh`
# still works without touching the file.
ENV_FILE="${ENV_FILE:-/etc/thutapi/env}"

if [[ -r "${ENV_FILE}" ]]; then
  # Sourcing executes the file, so it must be operator-owned and 0600 --
  # checked immediately below. set -a exports what it defines, which is
  # what the name-only --env form needs. Existing environment variables
  # still take precedence so one-off overrides work without modifying the file.
  _SAVED_GMI_API_KEY="${GMI_API_KEY:-}"
  _SAVED_PUBLIC_ORIGIN="${PUBLIC_ORIGIN:-}"
  _SAVED_UPLOAD_TOKEN="${UPLOAD_TOKEN:-}"
  set -a
  # shellcheck source=/dev/null
  . "${ENV_FILE}"
  set +a
  if [[ -n "${_SAVED_GMI_API_KEY}" ]]; then GMI_API_KEY="${_SAVED_GMI_API_KEY}"; fi
  if [[ -n "${_SAVED_PUBLIC_ORIGIN}" ]]; then PUBLIC_ORIGIN="${_SAVED_PUBLIC_ORIGIN}"; fi
  if [[ -n "${_SAVED_UPLOAD_TOKEN}" ]]; then UPLOAD_TOKEN="${_SAVED_UPLOAD_TOKEN}"; fi
fi

# A secrets file the whole box can read is not a secrets file. Warn rather
# than abort: an operator mid-incident should not be blocked by a chmod.
if [[ -r "${ENV_FILE}" ]]; then
  ENV_FILE_MODE="$(stat -c '%a' "${ENV_FILE}" 2>/dev/null || echo '')"
  case "${ENV_FILE_MODE}" in
    600|400|'') ;;
    *) echo "warning: ${ENV_FILE} is mode ${ENV_FILE_MODE}; want 600 (chmod 600 ${ENV_FILE})" >&2 ;;
  esac
fi

# GMI_API_KEY is the only required secret. Fail loudly if it is missing so
# the container cannot silently start with an unset inference key.
if [[ -z "${GMI_API_KEY:-}" ]]; then
  echo "error: GMI_API_KEY is not set and ${ENV_FILE} did not provide it." >&2
  echo "       Create it on the box:" >&2
  echo "         sudo install -d -m 0700 /etc/thutapi" >&2
  echo "         sudo install -m 0600 /dev/null /etc/thutapi/env" >&2
  echo "         sudo \$EDITOR /etc/thutapi/env    # GMI_API_KEY=..." >&2
  echo "       Avoid \`export GMI_API_KEY=...\` -- it persists in shell history." >&2
  echo "       The repo is public for the judging period; the key must never" >&2
  echo "       be hard-coded into this file or any image layer." >&2
  exit 1
fi

# Voice samples are fetched by GMI from this public HTTPS origin. Do not infer
# it from Host: a forwarded or hostile header would turn a child's recording
# into an incorrect provider URL. The Go process validates public HTTPS only.
if [[ -z "${PUBLIC_ORIGIN:-}" ]]; then
  echo "error: PUBLIC_ORIGIN is required (for example https://thutapi.nryn.dev)." >&2
  echo "       Set it in ${ENV_FILE}; it is not a secret." >&2
  exit 1
fi

# Generate an unguessable token for the voice-sample upload path (T13).
# The Hetzner box exposes POST /voice-sample for one-shot voice captures;
# GMI's Speech 2.8 source_audio fetches the resulting /media/<id> URL.
# Short-lived and unguessable keeps a child's voice off any directory
# listing. The process refuses to start without this token.
UPLOAD_TOKEN="${UPLOAD_TOKEN:-$(openssl rand -hex 16)}"

# T11's gate passcode is OPTIONAL. Unset means rate limits only, which is
# the shipping posture (internal/gate's package doc settles PLAN.md
# decision 9). Set it in ENV_FILE and re-run this script to add a shared
# passcode on the money routes without a code change. Docker's name-only
# --env form omits the variable entirely when it is unset, so an unset
# GATE_PASSCODE reaches the container as "not set" rather than as "".
#
# MEDIA_MAX_BYTES is T11's retention budget for generated media. Unset
# means the package default (6 GiB); the orphan sweep runs either way.
export GMI_API_KEY UPLOAD_TOKEN PUBLIC_ORIGIN
if [[ -n "${GATE_PASSCODE:-}" ]]; then export GATE_PASSCODE; fi
if [[ -n "${MEDIA_MAX_BYTES:-}" ]]; then export MEDIA_MAX_BYTES; fi

mkdir -p "${DATA_DIR}"
printf '%s' "${UPLOAD_TOKEN}" > "${DATA_DIR}/upload-token"
chmod 0600 "${DATA_DIR}/upload-token"

# The distroless runtime stage runs as uid 65532 ("nonroot") — see the
# Dockerfile's `USER nonroot:nonroot`. The bind mount is created by root
# here, so hand it to that uid or every write under /data fails with
# EACCES. 1000 is NOT the right uid for this image.
chown -R 65532:65532 "${DATA_DIR}"


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

  # Traefik's docker provider on foleyflow is configured with
  # `network: proxy` and `exposedByDefault: false` (/opt/traefik/static.yml).
  # A container not attached to `proxy` is discovered but has no reachable
  # address on that network, so the router 404s or 502s. Every sibling
  # (mosaic, cerebros, foleyflow, serp-relay) is on `proxy` and nothing else.
  --network "${NETWORK}"

  # Persistence: SQLite + generated media live on a host bind mount. The
  # container expects DATA_DIR and writes under it.
  --mount "type=bind,source=${DATA_DIR},target=/data"

  # Runtime configuration. PORT is what the binary listens on; loadbalancer
  # below points Traefik at the same port.
  --env "PORT=8080"
  --env "DATA_DIR=/data"

  # NAME-ONLY form, deliberately. `--env GMI_API_KEY` (no `=value`) makes
  # docker copy the value from this script's environment, so the secret
  # never enters the argument list and never shows up in `ps`. Writing
  # `--env "GMI_API_KEY=${GMI_API_KEY}"` here would undo that.
  --env GMI_API_KEY
  --env UPLOAD_TOKEN
  --env PUBLIC_ORIGIN

  # T11. Both optional; the name-only form passes them through only when
  # this script's environment actually has them. GATE_PASSCODE is a
  # secret when set, so it uses the same name-only form as the key above
  # and never enters the argument list.
  --env GATE_PASSCODE
  --env MEDIA_MAX_BYTES

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
# Pull explicitly so a moving tag like `latest` actually moves, and so a
# registry failure surfaces here rather than as a stale container.
if [[ "${IMAGE}" == *"/"* ]]; then
  docker pull "${IMAGE}"
fi
if docker inspect "${NAME}" >/dev/null 2>&1; then
  echo "Replacing existing container ${NAME}..."
  docker rm -f "${NAME}" >/dev/null
fi
docker run "${DOCKER_ARGS[@]}" "${IMAGE}"

echo
# Record exactly what ran. A tag is ambiguous over time; the digest is not.
echo
echo "Image digest actually running:"
docker inspect "${NAME}" --format '  {{.Image}}' 2>/dev/null || true
docker image inspect "${IMAGE}" --format '  {{index .RepoDigests 0}}' 2>/dev/null || true

echo "Container started. Useful follow-ups:"
echo "  docker logs -f ${NAME}"
echo "  curl -fsS https://thutapi.nryn.dev/healthz"
echo "  curl -fsS http://127.0.0.1:8080/healthz     # if HOST_PORT=8080"
echo "Voice-sample bearer (UPLOAD_TOKEN) — see ${DATA_DIR}/upload-token for the token; not echoed to stdout."
