#!/usr/bin/env bash
#
# deploy/redeploy_test.sh — Unit test suite for deploy/redeploy.sh.
#
# Tests preflight checks, permissions, missing/empty variables, argument
# handling, health gate assertions, rollback behavior, and image pruning.
# Uses isolated temporary directories and mock utilities (docker, curl) so
# it can run hermetically in CI or local workstations without docker daemon
# or root privileges.
#

set -euo pipefail

PASS_COUNT=0
FAIL_COUNT=0

RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REDEPLOY_SCRIPT="${SCRIPT_DIR}/redeploy.sh"

test_pass() {
  local name="$1"
  PASS_COUNT=$((PASS_COUNT + 1))
  echo -e "  [${GREEN}PASS${NC}] ${name}"
}

test_fail() {
  local name="$1"
  local reason="$2"
  FAIL_COUNT=$((FAIL_COUNT + 1))
  echo -e "  [${RED}FAIL${NC}] ${name}: ${reason}"
}

assert_exit_code() {
  local expected="$1"
  local actual="$2"
  local name="$3"
  if [[ "$expected" -eq "$actual" ]]; then
    test_pass "${name} (exit code ${actual})"
  else
    test_fail "${name}" "expected exit ${expected}, got ${actual}"
  fi
}

assert_output_contains() {
  local needle="$1"
  local haystack="$2"
  local name="$3"
  if [[ "$haystack" == *"$needle"* ]]; then
    test_pass "${name}"
  else
    test_fail "${name}" "output did not contain '${needle}'"
  fi
}

# Setup isolated test environment
TEST_TMP="$(mktemp -d)"
trap 'rm -rf "${TEST_TMP}"' EXIT

MOCK_BIN="${TEST_TMP}/mock_bin"
mkdir -p "${MOCK_BIN}"
MOCK_LOG="${TEST_TMP}/mock.log"

# Create mock docker CLI
cat <<'EOF' > "${MOCK_BIN}/docker"
#!/usr/bin/env bash
set -eu
ACTION="${1:-}"
shift || true
echo "docker ${ACTION} $*" >> "${MOCK_LOG}"

case "${ACTION}" in
  inspect)
    for arg in "$@"; do
      if [[ "$arg" == *"NetworkSettings.Networks"* ]]; then
        echo "${MOCK_CONTAINER_IP:-172.18.0.42}"
        exit 0
      elif [[ "$arg" == *"{{.Id}}"* ]]; then
        echo "${MOCK_CONTAINER_ID:-c0nt41n3r123456789}"
        exit 0
      elif [[ "$arg" == *"{{.Image}}"* ]]; then
        echo "${MOCK_CONTAINER_IMAGE:-sha256:1111222233334444}"
        exit 0
      fi
    done
    if [[ "${MOCK_CONTAINER_EXISTS:-1}" == "1" ]]; then
      exit 0
    else
      exit 1
    fi
    ;;
  image)
    SUB="${1:-}"
    shift || true
    if [[ "$SUB" == "inspect" ]]; then
      for arg in "$@"; do
        if [[ "$arg" == *"RepoDigests"* ]]; then
          echo "${MOCK_REPO_DIGEST:-ghcr.io/nrynss/thutapi@sha256:abcd1234ef012345}"
          exit 0
        fi
      done
      exit 0
    elif [[ "$SUB" == "prune" ]]; then
      echo "Total reclaimed space: 0B"
      exit 0
    fi
    ;;
  pull)
    if [[ "${MOCK_PULL_FAIL:-0}" == "1" ]]; then
      echo "Error response from daemon: repository not found" >&2
      exit 1
    fi
    echo "Status: Image is up to date"
    exit 0
    ;;
  rm)
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
EOF
chmod +x "${MOCK_BIN}/docker"

# Create mock curl CLI
cat <<'EOF' > "${MOCK_BIN}/curl"
#!/usr/bin/env bash
set -eu
echo "curl $*" >> "${MOCK_LOG}"

URL=""
for arg in "$@"; do
  if [[ "$arg" == http://* || "$arg" == https://* ]]; then
    URL="$arg"
  fi
done

if [[ "$URL" == *"/healthz" ]]; then
  if [[ "${MOCK_HEALTHZ_FAIL:-0}" == "1" ]]; then
    exit 7
  fi
  STATUS="${MOCK_HEALTHZ_STATUS:-ok}"
  VERSION="${MOCK_HEALTHZ_VERSION:-sha-1a2b3c4d5e}"
  printf '{"status":"%s","uptime_seconds":10,"version":"%s"}\n' "$STATUS" "$VERSION"
  exit 0
elif [[ "$URL" == *"8080/" ]]; then
  CODE="${MOCK_SHELF_CODE:-200}"
  for i in "$@"; do
    if [[ "$i" == *"%{http_code}"* ]]; then
      printf "%s" "$CODE"
      exit 0
    fi
  done
  if [[ "$CODE" == "200" ]]; then
    echo "<html>Shelf</html>"
    exit 0
  else
    exit 1
  fi
fi

exit 0
EOF
chmod +x "${MOCK_BIN}/curl"

# Create mock docker-run.sh
MOCK_DOCKER_RUN="${TEST_TMP}/mock-docker-run.sh"
cat <<'EOF' > "${MOCK_DOCKER_RUN}"
#!/usr/bin/env bash
set -eu
echo "docker-run IMAGE=${IMAGE:-} ENV_FILE=${ENV_FILE:-} NAME=${NAME:-}" >> "${MOCK_LOG}"
if [[ "${MOCK_DOCKER_RUN_FAIL:-0}" == "1" ]]; then
  echo "Simulated docker-run failure" >&2
  exit 1
fi
exit 0
EOF
chmod +x "${MOCK_DOCKER_RUN}"

export PATH="${MOCK_BIN}:${PATH}"
export MOCK_LOG

echo "Running deploy/redeploy.sh test suite..."
echo

# Helper to create valid env file
create_valid_env() {
  local target="$1"
  local mode="${2:-600}"
  cat <<EOF > "${target}"
GMI_API_KEY=test-inference-key-12345
UPLOAD_TOKEN=test-upload-token-abcdef
PUBLIC_ORIGIN=https://thutapi.nryn.dev
GATE_PASSCODE=optional-passcode
MEDIA_MAX_BYTES=6442450944
EOF
  chmod "${mode}" "${target}"
}

# -----------------------------------------------------------------------------
# Test Group 1: Preflight validation
# -----------------------------------------------------------------------------
echo "Test Group 1: Preflight checks on ENV_FILE"

# Test 1: Missing env file
set +e
OUT=$(ENV_FILE="${TEST_TMP}/nonexistent_env" "${REDEPLOY_SCRIPT}" 2>&1)
CODE=$?
set -e
assert_exit_code 1 "$CODE" "Preflight fails when ENV_FILE does not exist"
assert_output_contains "preflight failed" "$OUT" "Reports preflight failed on missing file"

# Test 2: Mode 644 (insecure permissions)
ENV_INSECURE="${TEST_TMP}/env_insecure"
create_valid_env "${ENV_INSECURE}" 644
set +e
OUT=$(ENV_FILE="${ENV_INSECURE}" "${REDEPLOY_SCRIPT}" 2>&1)
CODE=$?
set -e
assert_exit_code 1 "$CODE" "Preflight fails when ENV_FILE is mode 644"
assert_output_contains "expected 600 or 400" "$OUT" "Reports expected mode 600 or 400"

# Test 3: Mode 777 (world-writable)
ENV_777="${TEST_TMP}/env_777"
create_valid_env "${ENV_777}" 777
set +e
OUT=$(ENV_FILE="${ENV_777}" "${REDEPLOY_SCRIPT}" 2>&1)
CODE=$?
set -e
assert_exit_code 1 "$CODE" "Preflight fails when ENV_FILE is mode 777"

# Test 4: Missing GMI_API_KEY
ENV_NO_KEY="${TEST_TMP}/env_no_key"
cat <<EOF > "${ENV_NO_KEY}"
UPLOAD_TOKEN=token123
PUBLIC_ORIGIN=https://thutapi.nryn.dev
EOF
chmod 600 "${ENV_NO_KEY}"
set +e
OUT=$(ENV_FILE="${ENV_NO_KEY}" "${REDEPLOY_SCRIPT}" 2>&1)
CODE=$?
set -e
assert_exit_code 1 "$CODE" "Preflight fails when GMI_API_KEY is missing"
assert_output_contains "GMI_API_KEY" "$OUT" "Reports missing GMI_API_KEY"

# Test 5: Empty GMI_API_KEY
ENV_EMPTY_KEY="${TEST_TMP}/env_empty_key"
cat <<EOF > "${ENV_EMPTY_KEY}"
GMI_API_KEY=
UPLOAD_TOKEN=token123
PUBLIC_ORIGIN=https://thutapi.nryn.dev
EOF
chmod 600 "${ENV_EMPTY_KEY}"
set +e
OUT=$(ENV_FILE="${ENV_EMPTY_KEY}" "${REDEPLOY_SCRIPT}" 2>&1)
CODE=$?
set -e
assert_exit_code 1 "$CODE" "Preflight fails when GMI_API_KEY is empty"
assert_output_contains "GMI_API_KEY" "$OUT" "Reports empty GMI_API_KEY"

# Test 6: Missing UPLOAD_TOKEN
ENV_NO_TOKEN="${TEST_TMP}/env_no_token"
cat <<EOF > "${ENV_NO_TOKEN}"
GMI_API_KEY=key123
PUBLIC_ORIGIN=https://thutapi.nryn.dev
EOF
chmod 600 "${ENV_NO_TOKEN}"
set +e
OUT=$(ENV_FILE="${ENV_NO_TOKEN}" "${REDEPLOY_SCRIPT}" 2>&1)
CODE=$?
set -e
assert_exit_code 1 "$CODE" "Preflight fails when UPLOAD_TOKEN is missing"
assert_output_contains "UPLOAD_TOKEN" "$OUT" "Reports missing UPLOAD_TOKEN"

# Test 7: Missing PUBLIC_ORIGIN
ENV_NO_ORIGIN="${TEST_TMP}/env_no_origin"
cat <<EOF > "${ENV_NO_ORIGIN}"
GMI_API_KEY=key123
UPLOAD_TOKEN=token123
EOF
chmod 600 "${ENV_NO_ORIGIN}"
set +e
OUT=$(ENV_FILE="${ENV_NO_ORIGIN}" "${REDEPLOY_SCRIPT}" 2>&1)
CODE=$?
set -e
assert_exit_code 1 "$CODE" "Preflight fails when PUBLIC_ORIGIN is missing"
assert_output_contains "PUBLIC_ORIGIN" "$OUT" "Reports missing PUBLIC_ORIGIN"

# Test 8: Preflight succeeds with mode 400 (read-only)
ENV_VALID_400="${TEST_TMP}/env_valid_400"
create_valid_env "${ENV_VALID_400}" 400
: > "${MOCK_LOG}"
set +e
OUT=$(ENV_FILE="${ENV_VALID_400}" \
      DOCKER_RUN_SCRIPT="${MOCK_DOCKER_RUN}" \
      HEALTH_TIMEOUT=5 \
      "${REDEPLOY_SCRIPT}" "ghcr.io/nrynss/thutapi:v400" 2>&1)
CODE=$?
set -e
assert_exit_code 0 "$CODE" "Preflight succeeds when ENV_FILE is mode 400"

ENV_VALID_600="${TEST_TMP}/env_valid_600"
create_valid_env "${ENV_VALID_600}" 600

echo

# -----------------------------------------------------------------------------
# Test Group 2: Deployment success path and argument parsing
# -----------------------------------------------------------------------------
echo "Test Group 2: Successful deployment flow"

# Test 9: Full deploy with positional arguments
: > "${MOCK_LOG}"
export MOCK_HEALTHZ_STATUS="ok"
export MOCK_HEALTHZ_VERSION="sha-1a2b3c4d5e"
export MOCK_SHELF_CODE="200"
export MOCK_CONTAINER_IP="172.18.0.99"
export MOCK_REPO_DIGEST="ghcr.io/nrynss/thutapi@sha256:targetdigest999"

set +e
OUT=$(ENV_FILE="${ENV_VALID_600}" \
      DOCKER_RUN_SCRIPT="${MOCK_DOCKER_RUN}" \
      HEALTH_TIMEOUT=5 \
      "${REDEPLOY_SCRIPT}" "ghcr.io/nrynss/thutapi:v1.0" "sha-1a2b3c4d5e" 2>&1)
CODE=$?
set -e
assert_exit_code 0 "$CODE" "Deploy succeeds with positional arguments"
assert_output_contains "Deployment Successful" "$OUT" "Prints success banner"
assert_output_contains "Target Image: ghcr.io/nrynss/thutapi:v1.0" "$OUT" "Logs target image"
assert_output_contains "Expected SHA: sha-1a2b3c4d5e" "$OUT" "Logs expected SHA"
assert_output_contains "Resolved deployment image to: ghcr.io/nrynss/thutapi@sha256:targetdigest999" "$OUT" "Resolves repo digest"
assert_output_contains "Health gate passed" "$OUT" "Health gate confirmation"
assert_output_contains "GET / returned HTTP 200" "$OUT" "Shelf returned 200"

# Verify mock execution calls
assert_output_contains "docker pull ghcr.io/nrynss/thutapi:v1.0" "$(cat "${MOCK_LOG}")" "docker pull executed"
assert_output_contains "docker-run IMAGE=ghcr.io/nrynss/thutapi@sha256:targetdigest999" "$(cat "${MOCK_LOG}")" "docker-run executed with resolved digest"
assert_output_contains "docker image prune -f" "$(cat "${MOCK_LOG}")" "docker image prune executed"

# Test 9b: Deploy using environment variables IMAGE and EXPECTED_SHA
: > "${MOCK_LOG}"
set +e
OUT=$(ENV_FILE="${ENV_VALID_600}" \
      DOCKER_RUN_SCRIPT="${MOCK_DOCKER_RUN}" \
      IMAGE="ghcr.io/nrynss/thutapi:envtag" \
      EXPECTED_SHA="sha-env-999" \
      MOCK_HEALTHZ_VERSION="sha-env-999" \
      HEALTH_TIMEOUT=5 \
      "${REDEPLOY_SCRIPT}" 2>&1)
CODE=$?
set -e
assert_exit_code 0 "$CODE" "Deploy succeeds with env variables IMAGE and EXPECTED_SHA"
assert_output_contains "Target Image: ghcr.io/nrynss/thutapi:envtag" "$OUT" "Uses IMAGE env variable"
assert_output_contains "Expected SHA: sha-env-999" "$OUT" "Uses EXPECTED_SHA env variable"

echo

# -----------------------------------------------------------------------------
# Test Group 3: Health Gate Rollback Behaviors
# -----------------------------------------------------------------------------
echo "Test Group 3: Health Gate and Automated Rollback"

# Test 10: Rollback when docker-run fails
: > "${MOCK_LOG}"
export MOCK_DOCKER_RUN_FAIL=1
set +e
OUT=$(ENV_FILE="${ENV_VALID_600}" \
      DOCKER_RUN_SCRIPT="${MOCK_DOCKER_RUN}" \
      "${REDEPLOY_SCRIPT}" "ghcr.io/nrynss/thutapi:failrun" 2>&1)
CODE=$?
set -e
assert_exit_code 1 "$CODE" "Redeploy fails when docker-run fails"
assert_output_contains "Failed to start container" "$OUT" "Logs failure reason"
assert_output_contains "Initiating rollback" "$OUT" "Triggers rollback to previous image"
unset MOCK_DOCKER_RUN_FAIL

# Test 11: Rollback when /healthz times out / fails
: > "${MOCK_LOG}"
export MOCK_HEALTHZ_FAIL=1
set +e
OUT=$(ENV_FILE="${ENV_VALID_600}" \
      DOCKER_RUN_SCRIPT="${MOCK_DOCKER_RUN}" \
      HEALTH_TIMEOUT=2 \
      "${REDEPLOY_SCRIPT}" "ghcr.io/nrynss/thutapi:badhealth" 2>&1)
CODE=$?
set -e
assert_exit_code 1 "$CODE" "Redeploy fails when /healthz fails"
assert_output_contains "health check failed" "$OUT" "Logs health check failure"
assert_output_contains "Initiating rollback" "$OUT" "Triggers rollback on health check failure"
unset MOCK_HEALTHZ_FAIL

# Test 12: Rollback when commit SHA mismatches
: > "${MOCK_LOG}"
export MOCK_HEALTHZ_STATUS="ok"
export MOCK_HEALTHZ_VERSION="old-sha-111111"
set +e
OUT=$(ENV_FILE="${ENV_VALID_600}" \
      DOCKER_RUN_SCRIPT="${MOCK_DOCKER_RUN}" \
      HEALTH_TIMEOUT=2 \
      "${REDEPLOY_SCRIPT}" "ghcr.io/nrynss/thutapi:badversion" "expected-sha-222222" 2>&1)
CODE=$?
set -e
assert_exit_code 1 "$CODE" "Redeploy fails when SHA mismatches"
assert_output_contains "expected SHA='expected-sha-222222'" "$OUT" "Identifies mismatched SHA"
assert_output_contains "Initiating rollback" "$OUT" "Triggers rollback on SHA mismatch"

# Test 13: Rollback when GET / returns non-200 (e.g. 404)
: > "${MOCK_LOG}"
export MOCK_HEALTHZ_STATUS="ok"
export MOCK_HEALTHZ_VERSION="sha-match-123"
export MOCK_SHELF_CODE="404"
set +e
OUT=$(ENV_FILE="${ENV_VALID_600}" \
      DOCKER_RUN_SCRIPT="${MOCK_DOCKER_RUN}" \
      HEALTH_TIMEOUT=2 \
      "${REDEPLOY_SCRIPT}" "ghcr.io/nrynss/thutapi:shelf404" "sha-match-123" 2>&1)
CODE=$?
set -e
assert_exit_code 1 "$CODE" "Redeploy fails when GET / returns 404"
assert_output_contains "GET http://172.18.0.99:8080/ returned HTTP 404" "$OUT" "Detects 404 shelf page"
assert_output_contains "Initiating rollback" "$OUT" "Triggers rollback on shelf 404"

echo
echo "========================================="
echo "Test Summary: ${PASS_COUNT} passed, ${FAIL_COUNT} failed"
echo "========================================="

if [[ "${FAIL_COUNT}" -gt 0 ]]; then
  exit 1
fi
exit 0
