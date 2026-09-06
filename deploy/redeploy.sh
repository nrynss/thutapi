#!/usr/bin/env bash
#
# deploy/redeploy.sh — Production redeployment script for Thutapi.
#
# Automates deployment of Thutapi on the Hetzner box (foleyflow):
# 1. Preflight validation of /etc/thutapi/env (mode 600 or 400, required secrets).
# 2. Record currently running image/digest for automated rollback.
# 3. Pull target image and resolve to exact immutable RepoDigest.
# 4. Invoke /srv/thutapi/deploy/docker-run.sh with target image digest.
# 5. Health gate:
#    - Poll http://${CONTAINER_IP}:8080/healthz (up to 30s) asserting status "ok"
#      and matching commit SHA (if provided).
#    - Assert GET http://${CONTAINER_IP}:8080/ returns HTTP 200 (shelf page).
#    - On failure: roll back to previous image and exit 1.
# 6. Prune dangling images via `docker image prune -f`.
# 7. Output running container ID, image digest, and health status.
#
# Usage:
#   ./deploy/redeploy.sh [<image_ref>] [<expected_sha>]
#
# Examples:
#   ./deploy/redeploy.sh ghcr.io/nrynss/thutapi:1a2b3c4 1a2b3c4
#   ./deploy/redeploy.sh ghcr.io/nrynss/thutapi@sha256:...
#   IMAGE=ghcr.io/nrynss/thutapi:latest EXPECTED_SHA=abc1234 ./deploy/redeploy.sh
#

set -euo pipefail

ENV_FILE="${ENV_FILE:-/etc/thutapi/env}"
NAME="${NAME:-thutapi}"
DOCKER_RUN_SCRIPT="${DOCKER_RUN_SCRIPT:-/srv/thutapi/deploy/docker-run.sh}"
HEALTH_TIMEOUT="${HEALTH_TIMEOUT:-30}"

# Target image reference and optional expected commit SHA
TARGET_IMAGE="${1:-${IMAGE:-ghcr.io/nrynss/thutapi:latest}}"
EXPECTED_SHA="${2:-${EXPECTED_SHA:-${SHA:-}}}"

echo "=== Starting Thutapi Redeploy ==="
echo "Target Image: ${TARGET_IMAGE}"
if [[ -n "${EXPECTED_SHA}" ]]; then
  echo "Expected SHA: ${EXPECTED_SHA}"
fi

# -----------------------------------------------------------------------------
# 1. Preflight check: inspect env file
# -----------------------------------------------------------------------------
echo "--- Preflight: checking ${ENV_FILE} ---"

if [[ ! -f "${ENV_FILE}" ]]; then
  echo "error: preflight failed: env file ${ENV_FILE} does not exist or is not a regular file." >&2
  exit 1
fi

ENV_MODE="$(stat -c '%a' "${ENV_FILE}" 2>/dev/null || stat -f '%Op' "${ENV_FILE}" 2>/dev/null || echo '')"
ENV_MODE="${ENV_MODE: -3}"
case "${ENV_MODE}" in
  600|400)
    # Mode is strictly restricted to owner-only read or read/write
    ;;
  *)
    echo "error: preflight failed: ${ENV_FILE} is mode ${ENV_MODE}, expected 600 or 400." >&2
    echo "       Fix permissions: sudo chmod 600 ${ENV_FILE}" >&2
    exit 1
    ;;
esac

if [[ ! -r "${ENV_FILE}" ]]; then
  echo "error: preflight failed: ${ENV_FILE} is not readable by current user ($(id -un))." >&2
  exit 1
fi

# Verify required variables are defined and non-empty in the env file.
# Executed in a clean environment so outer shell variables cannot mask missing keys.
# No secret values are printed to stdout/stderr.
PREFLIGHT_ERR="$(env -i bash -c "
  set -eu
  . \"${ENV_FILE}\"
  missing=()
  [[ -z \"\${GMI_API_KEY:-}\" ]] && missing+=(\"GMI_API_KEY\")
  [[ -z \"\${UPLOAD_TOKEN:-}\" ]] && missing+=(\"UPLOAD_TOKEN\")
  [[ -z \"\${PUBLIC_ORIGIN:-}\" ]] && missing+=(\"PUBLIC_ORIGIN\")
  if [[ \${#missing[@]} -gt 0 ]]; then
    echo \"required variable(s) missing or empty: \${missing[*]}\"
    exit 1
  fi
" 2>&1 || true)"

if [[ -n "${PREFLIGHT_ERR}" ]]; then
  echo "error: preflight failed: ${ENV_FILE}: ${PREFLIGHT_ERR}" >&2
  exit 1
fi

echo "Preflight check passed."

# Locate docker-run.sh
if [[ ! -x "${DOCKER_RUN_SCRIPT}" ]]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  if [[ -x "${SCRIPT_DIR}/docker-run.sh" ]]; then
    DOCKER_RUN_SCRIPT="${SCRIPT_DIR}/docker-run.sh"
  else
    echo "error: docker-run script not found or not executable at ${DOCKER_RUN_SCRIPT}" >&2
    exit 1
  fi
fi

# -----------------------------------------------------------------------------
# 2. Record current running container state for rollback
# -----------------------------------------------------------------------------
echo "--- Inspecting running container ---"
PREV_CONTAINER_ID="$(docker inspect "${NAME}" --format '{{.Id}}' 2>/dev/null || true)"
PREV_IMAGE_ID="$(docker inspect "${NAME}" --format '{{.Image}}' 2>/dev/null || true)"
PREV_DIGEST=""
if [[ -n "${PREV_IMAGE_ID}" ]]; then
  PREV_DIGEST="$(docker image inspect "${PREV_IMAGE_ID}" --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}' 2>/dev/null || true)"
fi

ROLLBACK_IMAGE="${PREV_DIGEST:-${PREV_IMAGE_ID}}"

if [[ -n "${PREV_CONTAINER_ID}" ]]; then
  echo "Recorded running container ${PREV_CONTAINER_ID:0:12}"
  echo "Rollback target: ${ROLLBACK_IMAGE}"
else
  echo "No existing ${NAME} container running; rollback unavailable if deploy fails."
fi

# Rollback helper function
rollback() {
  local reason="$1"
  echo "error: ${reason}" >&2
  if [[ -n "${ROLLBACK_IMAGE}" ]]; then
    echo "Initiating rollback to previous image: ${ROLLBACK_IMAGE}..." >&2
    if IMAGE="${ROLLBACK_IMAGE}" ENV_FILE="${ENV_FILE}" NAME="${NAME}" "${DOCKER_RUN_SCRIPT}"; then
      echo "Rollback to ${ROLLBACK_IMAGE} completed." >&2
    else
      echo "CRITICAL: Rollback to ${ROLLBACK_IMAGE} failed!" >&2
    fi
  else
    echo "No previous running image recorded; cannot roll back." >&2
  fi
  exit 1
}

# -----------------------------------------------------------------------------
# 3. Pull target image and resolve RepoDigest
# -----------------------------------------------------------------------------
echo "--- Pulling target image: ${TARGET_IMAGE} ---"
if [[ "${TARGET_IMAGE}" == *"/"* ]]; then
  docker pull "${TARGET_IMAGE}"
fi

RESOLVED_DIGEST=""
if [[ "${TARGET_IMAGE}" == *"@"* ]]; then
  DEPLOY_IMAGE="${TARGET_IMAGE}"
else
  RESOLVED_DIGEST="$(docker image inspect "${TARGET_IMAGE}" --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}' 2>/dev/null || true)"
  DEPLOY_IMAGE="${RESOLVED_DIGEST:-${TARGET_IMAGE}}"
fi

echo "Resolved deployment image to: ${DEPLOY_IMAGE}"

# -----------------------------------------------------------------------------
# 4. Execute docker-run.sh
# -----------------------------------------------------------------------------
echo "--- Starting new container with ${DEPLOY_IMAGE} ---"
if ! IMAGE="${DEPLOY_IMAGE}" ENV_FILE="${ENV_FILE}" NAME="${NAME}" "${DOCKER_RUN_SCRIPT}"; then
  rollback "Failed to start container with ${DEPLOY_IMAGE}"
fi

# -----------------------------------------------------------------------------
# 5. Health Gate
# -----------------------------------------------------------------------------
echo "--- Health Gate ---"
CONTAINER_IP=""
for i in $(seq 1 10); do
  CONTAINER_IP="$(docker inspect "${NAME}" --format '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' 2>/dev/null || true)"
  if [[ -n "${CONTAINER_IP}" ]]; then
    break
  fi
  sleep 1
done

if [[ -z "${CONTAINER_IP}" ]]; then
  rollback "Could not obtain container IP for ${NAME} on Docker network"
fi
echo "Container IP on network: ${CONTAINER_IP}"

echo "Polling http://${CONTAINER_IP}:8080/healthz (timeout ${HEALTH_TIMEOUT}s)..."
HEALTHZ_OK=0
HEALTH_VERSION=""
HEALTH_STATUS=""
START_TIME=$(date +%s)

while true; do
  NOW=$(date +%s)
  ELAPSED=$((NOW - START_TIME))
  if [[ $ELAPSED -ge $HEALTH_TIMEOUT ]]; then
    break
  fi

  HTTP_RES="$(curl -s -S --max-time 2 "http://${CONTAINER_IP}:8080/healthz" 2>/dev/null || true)"
  if [[ -n "${HTTP_RES}" ]]; then
    if command -v python3 >/dev/null 2>&1; then
      HEALTH_STATUS="$(python3 -c 'import sys, json; print(json.loads(sys.argv[1]).get("status", ""))' "${HTTP_RES}" 2>/dev/null || echo "")"
      HEALTH_VERSION="$(python3 -c 'import sys, json; print(json.loads(sys.argv[1]).get("version", ""))' "${HTTP_RES}" 2>/dev/null || echo "")"
    else
      HEALTH_STATUS="$(echo "${HTTP_RES}" | sed -n 's/.*"status":[ ]*"\([^"]*\)".*/\1/p')"
      HEALTH_VERSION="$(echo "${HTTP_RES}" | sed -n 's/.*"version":[ ]*"\([^"]*\)".*/\1/p')"
    fi

    if [[ "${HEALTH_STATUS}" == "ok" ]]; then
      if [[ -n "${EXPECTED_SHA}" ]]; then
        if [[ "${HEALTH_VERSION}" == "${EXPECTED_SHA}"* || "${EXPECTED_SHA}" == "${HEALTH_VERSION}"* ]]; then
          HEALTHZ_OK=1
          break
        fi
      else
        HEALTHZ_OK=1
        break
      fi
    fi
  fi
  sleep 1
done

if [[ ${HEALTHZ_OK} -ne 1 ]]; then
  rollback "health check failed on /healthz after ${HEALTH_TIMEOUT}s (status='${HEALTH_STATUS}', version='${HEALTH_VERSION}', expected SHA='${EXPECTED_SHA}')"
fi
echo "✓ /healthz responded with status='ok', version='${HEALTH_VERSION}'"

echo "Checking shelf page: GET http://${CONTAINER_IP}:8080/ ..."
SHELF_CODE="$(curl -s -o /dev/null -w "%{http_code}" --max-time 5 "http://${CONTAINER_IP}:8080/" 2>/dev/null || echo "000")"
if [[ "${SHELF_CODE}" != "200" ]]; then
  rollback "GET http://${CONTAINER_IP}:8080/ returned HTTP ${SHELF_CODE}, expected 200"
fi
echo "✓ GET / returned HTTP 200"
echo "Health gate passed."

# -----------------------------------------------------------------------------
# 6. Pruning dangling layers
# -----------------------------------------------------------------------------
echo "--- Pruning dangling images ---"
docker image prune -f || true

# -----------------------------------------------------------------------------
# 7. Log final output
# -----------------------------------------------------------------------------
FINAL_CONTAINER_ID="$(docker inspect "${NAME}" --format '{{.Id}}' 2>/dev/null || echo "unknown")"
FINAL_IMAGE_ID="$(docker inspect "${NAME}" --format '{{.Image}}' 2>/dev/null || echo "unknown")"
FINAL_DIGEST="$(docker image inspect "${FINAL_IMAGE_ID}" --format '{{if .RepoDigests}}{{index .RepoDigests 0}}{{end}}' 2>/dev/null || echo "${FINAL_IMAGE_ID}")"

echo
echo "================ Deployment Successful ================"
echo "Container Name:   ${NAME}"
echo "Container ID:     ${FINAL_CONTAINER_ID}"
echo "Image ID:         ${FINAL_IMAGE_ID}"
echo "Image Digest:     ${FINAL_DIGEST}"
echo "Health Status:    ${HEALTH_STATUS}"
echo "Deployed Version: ${HEALTH_VERSION}"
echo "Shelf Page:       HTTP 200"
echo "========================================================"

exit 0
