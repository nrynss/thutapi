# T1b round 1 — live deployment verification

| | |
|---|---|
| **Target** | T1b at main HEAD. T1 closed at `65df379`. T1b unblocks T2 (via artifacts in T1) and T13 + T14 (via the live URL). |
| **Author** | Operator with DNS credentials for `nryn.dev` and SSH access to `foleyflow`. No agent on this workstation can do this work. |
| **Method** | Add the proxied A record, SSH to the box, run `deploy/docker-run.sh`, paste the five transcripts below. |

## Status

**RUN 2026-09-04 15:20 UTC. BLOCKED at check 2 on a dead Cloudflare token.**

| Check | Verdict |
|---|---|
| 1 — Traefik picks the container up | **PASS** |
| 2 — DNS A record | **BLOCKED** — cannot write the record; token invalid |
| 3 — Let's Encrypt certificate issued | **FAIL** — DNS-01 returns HTTP 401 |
| 4 — Live curl with `cf-ray` | **BLOCKED** on 2 and 3 |
| 5 — Public file fetchable by a third party | **BLOCKED** on 2 and 3 |

Routing itself is proven: the container answers correctly both directly
and through Traefik at the origin (transcripts under check 1). What is
missing is entirely on the Cloudflare side.

## The blocker — `CLOUDFLARE_DNS_API_TOKEN` on foleyflow is invalid

The token in the Traefik container's environment is rejected by Cloudflare.
It is 53 characters, matches `^[A-Za-z0-9_-]+$`, and has no leading or
trailing whitespace, so this is not a quoting artefact — it is dead:

```
$ curl -sS https://api.cloudflare.com/client/v4/user/tokens/verify -H "Authorization: Bearer $CLOUDFLARE_DNS_API_TOKEN"
{"success":false,"errors":[{"code":1000,"message":"Invalid API Token"}],"messages":[],"result":null}
```

Traefik's own ACME run confirms it independently. Starting the container
made lego attempt issuance immediately, and it failed writing the
challenge record:

```
INF Obtaining bundled SAN certificate.  domains=thutapi.nryn.dev lib=lego
INF Use solver.                         domain=thutapi.nryn.dev lib=lego type=dns-01
INF dns01: preparing to solve the challenge. domain=thutapi.nryn.dev lib=lego
ERR Unable to obtain ACME certificate for domains
    error="unable to generate a certificate for the domains [thutapi.nryn.dev]:
    resolver: one or more domains had a problem: [thutapi.nryn.dev:
    dns01: error presenting token (thutapi.nryn.dev): cloudflare:
    failed to create TXT record: [status code 401] 10000: Authentication error]"
    routerName=thutapi@docker rule=Host(`thutapi.nryn.dev`)
```

**The blast radius is the whole box, not Thutapi.** The same log line
appeared for `serp.nryn.dev` in the same second, so `serp` has *already*
been failing issuance and is serving `CN=TRAEFIK DEFAULT CERT` at the
origin — invisible from outside only because Cloudflare's edge cert
fronts it. `auteur`, `eoc` and `mosaic` hold Let's Encrypt certs issued
about three weeks ago and will fail the same way when they come up for
renewal.

**Fix:** mint a Cloudflare API token with `Zone → DNS → Edit` on the
`nryn.dev` zone, recreate the Traefik container with it in
`CLOUDFLARE_DNS_API_TOKEN`, then add the `thutapi` A record and re-run
checks 2–5. This unblocks T1b and repairs renewal for the other four
sites at the same time.

## Five live checks (paste each command + output below)

### Check 1 — Traefik picks the container up

```bash
ssh foleyflow 'docker ps --filter name=thutapi --format "{{.Names}} {{.Status}} {{.Labels}}"' \
  | tr ' ' '\n' | grep traefik
```

Expected: all five labels present, container status `Up ...`.

**PASS 2026-09-04.** Container up, attached to the `proxy` network, all
five labels byte-identical to project.md §Deployment:

```
$ docker ps --filter name=thutapi --format "{{.Names}}|{{.Status}}"
thutapi|Up 24 seconds

$ docker inspect thutapi --format '{{range $k,$v := .NetworkSettings.Networks}}{{$k}}={{$v.IPAddress}} {{end}}'
proxy=172.18.0.8

$ docker inspect thutapi --format '{{range $k,$v := .Config.Labels}}{{$k}}={{$v}}{{println}}{{end}}'
traefik.enable=true
traefik.http.routers.thutapi.entrypoints=websecure
traefik.http.routers.thutapi.rule=Host(`thutapi.nryn.dev`)
traefik.http.routers.thutapi.tls.certresolver=letsencrypt
traefik.http.services.thutapi.loadbalancer.server.port=8080

$ docker logs thutapi --tail 1
{"time":"2026-09-04T15:20:04.270933406Z","level":"INFO","msg":"thutapi listening","addr":"0.0.0.0:8080"}
```

Routing is proven end to end at the origin, which is as far as it can be
proven while check 2 is blocked. Directly to the container, and through
Traefik with the public DNS step bypassed:

```
$ docker run --rm --network proxy curlimages/curl:latest -sS -i http://172.18.0.8:8080/healthz
HTTP/1.1 200 OK
Content-Type: application/json
{"status":"ok","uptime_seconds":36,"version":"t1b-cfa8bd4"}

$ curl -k -sS -i --resolve thutapi.nryn.dev:443:127.0.0.1 https://thutapi.nryn.dev/healthz
HTTP/2 200
content-type: application/json
{"status":"ok","uptime_seconds":36,"version":"t1b-cfa8bd4"}
```

Traefik's access log attributes the request to the right router and
backend, which is the fact check 1 actually exists to establish:

```
172.18.0.1 - - [04/Sep/2026:15:20:40 +0000] "GET /healthz HTTP/2.0" 200 59 "-" "-" 29071 "thutapi@docker" "http://172.18.0.8:8080" 2ms
```

`-k` is required above and is not a papered-over failure: it is check 3
failing, recorded separately below. The `version` field reading
`t1b-cfa8bd4` also closes the last of T1's L1 — `--build-arg VERSION`
reaches `main.version` and out to the wire.

### Defects this run found in the T1 artifacts

**D1 — `deploy/docker-run.sh` was missing `--network proxy`.** Traefik's
docker provider on the box is configured `exposedByDefault: false` with
`network: proxy`, so a container outside that network is discovered but
has no address Traefik can route to. Every sibling (`mosaic`, `cerebros`,
`foleyflow`, `serp-relay`) is on `proxy` and nothing else. Fixed; the
container above is on `proxy` because of that fix.

**D2 — the operator runbook's `chown 1000:1000` was the wrong uid.** The
runtime stage is `gcr.io/distroless/base-debian12:nonroot` and the
Dockerfile ends `USER nonroot:nonroot`, which is **uid 65532**. At 1000
every write under `/data` fails with `EACCES`. Fixed in
`deploy/docker-run.sh`, which now chowns the bind mount itself rather than
leaving it to a hand-typed command.

### Check 2 — DNS A record

```bash
dig +short thutapi.nryn.dev A
dig @1.1.1.1 +short thutapi.nryn.dev A
```

Expected: a Cloudflare-proxied answer — `104.21.x` / `172.67.x`, matching
`auteur` / `mosaic` / `eoc` / `serp`.

> **Corrected 2026-09-04.** This check previously demanded the origin IP
> `167.233.247.107` and said proxied answers "do not count", on the
> assumption that Let's Encrypt validates over HTTP-01. It does not.
> `/opt/traefik/static.yml` on the box configures
> `certificatesResolvers.letsencrypt.acme.dnsChallenge` with
> `provider: cloudflare` — issuance is **DNS-01**, which writes a
> `_acme-challenge` TXT record and never needs the origin IP to be
> publicly resolvable. Proxied is therefore correct *and* required
> (project.md §Deployment and the 100s/524 architecture both assume the
> orange cloud). Confirm the record is proxied at the API/dashboard, not
> by reading `dig`.

### Check 3 — Let's Encrypt certificate issued

Run this **on the box**, against the origin — not from the public internet:

```bash
ssh foleyflow "echo | openssl s_client -servername thutapi.nryn.dev -connect 127.0.0.1:443 2>/dev/null \
  | openssl x509 -noout -subject -issuer -dates"
```

Expected: subject `CN=thutapi.nryn.dev` (or SAN containing it); issuer
`Let's Encrypt ... R3/R10/R11`; `notAfter` in the future.

> **Corrected 2026-09-04.** This check previously probed
> `thutapi.nryn.dev:443` from the public internet and expected a Let's
> Encrypt issuer. It cannot pass there. The records are Cloudflare-proxied,
> so a public TLS handshake terminates at the **Cloudflare edge** and
> returns Cloudflare's own certificate. Measured on all four existing
> subdomains the same afternoon: `auteur`, `mosaic`, `eoc` and `serp` all
> present `issuer=C=US, O=Google Trust Services, CN=WE1`, expiring
> `Nov 19 06:56:04 2026 GMT` — one shared edge cert, no Let's Encrypt
> anywhere in the public chain. Traefik's Let's Encrypt certificate is the
> **origin** cert, the one Cloudflare validates under Full (strict), and is
> only observable from the box itself.

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