#!/usr/bin/env bash
#
# Thutapi — finish T1b once a valid Cloudflare token exists.
#
# T1b's check 1 passed on 2026-09-04; checks 2-5 were blocked because the
# CLOUDFLARE_DNS_API_TOKEN in Traefik's environment on foleyflow is
# invalid (Cloudflare `code 1000`, and lego's DNS-01 401s). This script
# takes a fresh token and drives the rest of the track to a verdict.
#
# The token must have **Zone -> DNS -> Edit** on the `nryn.dev` zone.
#
# Run ON THE BOX, with the token on stdin so it never reaches argv or a
# shell history:
#
#   printf '%s\n' "$NEW_TOKEN" | ssh foleyflow 'bash -s' < deploy/finish-t1b.sh
#
# What it does, in order:
#   1. verifies the token against Cloudflare before touching anything
#   2. rewrites Traefik's env with it and recreates the container
#      (same image, same mounts, same published ports)
#   3. creates or updates the proxied A record thutapi -> 167.233.247.107
#   4. waits for Traefik to issue the origin cert by DNS-01
#   5. runs T1b checks 2, 3, 4 and 5 and prints a verdict per check
#
# Refs: dev-diary/adversarial-review/t1b-round1.md
#       dev-diary/PLAN.md §T1b
set -uo pipefail

ORIGIN_IP="167.233.247.107"
HOST="thutapi.nryn.dev"
ZONE_NAME="nryn.dev"

read -r CF_TOKEN
if [[ -z "${CF_TOKEN}" ]]; then
  echo "error: no token on stdin" >&2; exit 1
fi

api() {  # api <method> <path> [body]
  curl -sS -X "$1" "https://api.cloudflare.com/client/v4$2" \
    -H "Authorization: Bearer ${CF_TOKEN}" \
    -H "Content-Type: application/json" \
    ${3:+--data "$3"}
}

echo "=== 0. verify token ==="
if ! api GET /user/tokens/verify | grep -q '"success":true'; then
  echo "FAIL: token rejected by Cloudflare. Nothing changed." >&2
  api GET /user/tokens/verify >&2; exit 1
fi
echo "token OK"

ZONE=$(api GET "/zones?name=${ZONE_NAME}" | grep -oE '"id":"[a-f0-9]{32}"' | head -1 | cut -d'"' -f4)
[[ -n "${ZONE}" ]] || { echo "FAIL: no zone id for ${ZONE_NAME} (token may lack zone read)" >&2; exit 1; }
echo "zone: ${ZONE}"

echo
echo "=== 1. install token into Traefik and recreate ==="
# The flags below were read back off the running container on 2026-09-04
# and reproduce it exactly: image traefik:v3, network proxy, ports 80/443,
# restart unless-stopped, and the three bind mounts (acme rw, docker.sock
# ro, static.yml ro). Cmd/Entrypoint are the image defaults, so they are
# deliberately not overridden. Re-verify with
# `docker inspect traefik` before running this if the box has changed.
#
# This restarts the edge proxy: auteur, mosaic, eoc and serp all go down
# for the few seconds it takes to come back. Nothing else on the box is
# touched.
docker rm -f traefik >/dev/null
docker run -d --name traefik --restart unless-stopped \
  --network proxy \
  -p 80:80 -p 443:443 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v /opt/traefik/static.yml:/etc/traefik/traefik.yml:ro \
  -v /opt/traefik/acme:/acme \
  -e "CLOUDFLARE_DNS_API_TOKEN=${CF_TOKEN}" \
  traefik:v3 >/dev/null
echo "traefik recreated; waiting 10s for provider sync"
sleep 10

echo
echo "=== 2. A record ${HOST} -> ${ORIGIN_IP} (proxied) ==="
REC=$(api GET "/zones/${ZONE}/dns_records?type=A&name=${HOST}" \
  | grep -oE '"id":"[a-f0-9]{32}"' | head -1 | cut -d'"' -f4)
BODY="{\"type\":\"A\",\"name\":\"${HOST}\",\"content\":\"${ORIGIN_IP}\",\"proxied\":true,\"ttl\":1}"
if [[ -n "${REC}" ]]; then
  echo "record exists (${REC}) — updating"
  api PUT "/zones/${ZONE}/dns_records/${REC}" "${BODY}" | head -c 200; echo
else
  api POST "/zones/${ZONE}/dns_records" "${BODY}" | head -c 200; echo
fi
echo "--- resolves to ---"
getent hosts "${HOST}" || echo "(not resolving here yet)"

echo
echo "=== 3. origin certificate (DNS-01) ==="
# Nudge Traefik into issuing, then poll. DNS-01 is typically 30-90s.
curl -k -sS -o /dev/null --resolve "${HOST}:443:127.0.0.1" "https://${HOST}/healthz" || true
for i in $(seq 1 20); do
  SUBJ=$(echo | openssl s_client -servername "${HOST}" -connect 127.0.0.1:443 2>/dev/null \
    | openssl x509 -noout -subject -issuer -dates 2>/dev/null)
  if ! grep -q "TRAEFIK DEFAULT CERT" <<<"${SUBJ}"; then
    echo "${SUBJ}"; break
  fi
  echo "  attempt ${i}/20: still TRAEFIK DEFAULT CERT, retrying in 15s"
  sleep 15
  curl -k -sS -o /dev/null --resolve "${HOST}:443:127.0.0.1" "https://${HOST}/healthz" || true
done
grep -q "TRAEFIK DEFAULT CERT" <<<"${SUBJ}" && {
  echo "FAIL: cert never issued. docker logs traefik | grep -i acme"; }

echo
echo "=== 4. public curl (expect 200 + cf-ray) ==="
curl -fsS -i "https://${HOST}/healthz" 2>&1 | head -15

echo
echo "=== 5. public file fetchable by a third party ==="
echo "thutapi-t13-smoke" > /srv/thutapi/data/upload-probe.txt
chown 65532:65532 /srv/thutapi/data/upload-probe.txt
echo "probe written to /srv/thutapi/data/upload-probe.txt"
echo "NOTE: the T1 stub serves only /healthz — it has no static file route yet."
echo "      Check 5 cannot pass until T3 lands the media handler. Fetch then:"
echo "        curl -fsS https://${HOST}/media/upload-probe.txt"
