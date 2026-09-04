# T1b round 1 — live deployment verification

| | |
|---|---|
| **Target** | T1b at main HEAD. T1 closed at `65df379`. T1b unblocks T2 (via artifacts in T1) and T13 + T14 (via the live URL). |
| **Author** | Operator with DNS credentials for `nryn.dev` and SSH access to `foleyflow`. No agent on this workstation can do this work. |
| **Method** | Add the proxied A record, SSH to the box, run `deploy/docker-run.sh`, paste the five transcripts below. |

## Status

**NOT STARTED.** Paste the five transcripts once each is run.

## Five live checks (paste each command + output below)

### Check 1 — Traefik picks the container up

```bash
ssh foleyflow 'docker ps --filter name=thutapi --format "{{.Names}} {{.Status}} {{.Labels}}"' \
  | tr ' ' '\n' | grep traefik
```

Expected: all five labels present, container status `Up ...`.

### Check 2 — DNS A record

```bash
dig +short thutapi.nryn.dev A
dig @1.1.1.1 +short thutapi.nryn.dev A
```

Expected: `167.233.247.107`. Cloudflare-proxied answers (`104.21.x` / `172.67.x`)
do **not** count — the public DNS resolver must return the origin IP for the
Let's Encrypt HTTP-01 challenge to land.

### Check 3 — Let's Encrypt certificate issued

```bash
echo | openssl s_client -servername thutapi.nryn.dev -connect thutapi.nryn.dev:443 2>/dev/null \
  | openssl x509 -noout -subject -issuer -dates
```

Expected: subject `CN=thutapi.nryn.dev` (or SAN containing it); issuer
`Let's Encrypt ... R3/R10/R11`; `notAfter` in the future.

### Check 4 — Live curl with cf-ray

```bash
curl -fsS -i https://thutapi.nryn.dev/healthz
```

Expected: `HTTP/2 200`, `cf-ray:` header present, body
`{"status":"ok","uptime_seconds":...,"version":"dev"}` (version field
reflects whatever `--build-arg VERSION=...` was passed at build time —
set it to a real tag in `deploy/docker-run.sh` for the production build).

### Check 5 — Public file fetchable by a third party

```bash
# On the box, place a dummy file at a signed path:
ssh foleyflow 'echo "thutapi-t13-smoke" > /srv/thutapi/data/upload-probe.txt'
URL="https://thutapi.nryn.dev/upload-probe.txt"
# From any third party (not this workstation, not the box), fetch it:
curl -fsS -i "$URL"
```

Expected: 200 OK with body `thutapi-t13-smoke`. This is the exact
mechanism Speech 2.8's `source_audio` will rely on at T14.

## When all five pass

Mark T1b status DONE in `dev-diary/PLAN.md` (line 67 of the Status
table). The reviewer loop is then `t1b-round1.md` →
`t1b-remediation-round1.md` (only if a check fails) → `t1b-round2.md` →
APPROVE. Once APPROVE, T13 and T14 are unblocked.

## Operator notes

* DNS at Cloudflare: `nryn.dev` zone, proxied (orange cloud), A
  record `thutapi` → `167.233.247.107`. Same shape as `auteur`,
  `mosaic`, `eoc`, `serp`.
* Cloudflare SSL mode for the zone must be `Full (strict)`. Flexible
  plus Traefik's HTTPS redirect is an infinite redirect loop.
* `deploy/docker-run.sh` requires `GMI_API_KEY` in env. Read it from
  the operator vault, never check it in.
* The container listens on `0.0.0.0:8080` internally; Traefik routes by
  label. No host-port mapping required.
* Generated media accumulates under `/srv/thutapi/data` (mounted at
  `/data` inside the container per `deploy/docker-run.sh`).
* If `curl /healthz` returns HTTP 524, the 100s Cloudflare proxy
  timeout fired — but `/healthz` is sub-second, so the cause is
  upstream (Traefik not picking the container). Check Traefik logs:
  `ssh foleyflow 'docker logs traefik --tail 50'`.