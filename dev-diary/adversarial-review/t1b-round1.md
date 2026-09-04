# T1b round 1 — live deployment verification

| | |
|---|---|
| **Target** | T1b at main HEAD. T1 closed at `65df379`. T1b unblocks T2 (via artifacts in T1) and T13 + T14 (via the live URL). |
| **Author** | Operator with DNS credentials for `nryn.dev` and SSH access to `foleyflow`. No agent on this workstation can do this work. |
| **Method** | Add the proxied A record, SSH to the box, run `deploy/docker-run.sh`, paste the five transcripts below. |

## Status

**RUN 2026-09-04 15:20-15:30 UTC. The live URL is up.**
**https://thutapi.nryn.dev/healthz returns 200.**

| Check | Verdict |
|---|---|
| 1 — Traefik picks the container up | **PASS** |
| 2 — DNS A record | **PASS** — proxied A record created |
| 3 — Let's Encrypt certificate issued | **FAIL** — DNS-01 401s; origin serves `CN=TRAEFIK DEFAULT CERT` |
| 4 — Live curl with `cf-ray` | **PASS** |
| 5 — Public file fetchable by a third party | **MIS-SCOPED** — belongs to T3; the T1 stub has no file route |

Check 4 passes *despite* check 3 failing, and the reason matters — see
"Full, not Full (strict)" below. T1b's purpose was to prove the
environmental facts cheaply, and it did: the live URL works, and it
surfaced one real defect in the box's cert posture that would otherwise
have been discovered on Sunday.

Routing itself is proven: the container answers correctly both directly
and through Traefik at the origin (transcripts under check 1). Everything
still missing is on the Cloudflare side, except check 5, which was written
against a capability T1 was never going to have.

`deploy/finish-t1b.sh` drives checks 2-4 to a verdict in one run once a
valid token exists. It verifies the token *before* touching anything, so
a bad token changes nothing on the box.

## Full, not Full (strict) — why check 4 passes over a self-signed origin

`dev-diary/PLAN.md`, `deploy/README.md` and `project.md` all state that the
zone's SSL mode "must be Full (strict)" and that it "should already be
correct". **It is not.** The zone is on plain **Full**:

```
$ curl -sS https://api.cloudflare.com/client/v4/zones/$ZONE/settings/ssl -H "Authorization: Bearer $CF"
{"result":{"id":"ssl","value":"full","certificate_status":"active","editable":true},"success":true}
```

Full encrypts Cloudflare→origin but **does not validate the origin
certificate**. That is precisely why `https://thutapi.nryn.dev/healthz`
returns 200 while Traefik is serving a self-signed
`CN=TRAEFIK DEFAULT CERT`. Under Full (strict) the same request would be a
**526**.

**This is a loaded gun, and it points at every site on the box.** Anyone
who reads the plan, notices the zone is not on strict, and "corrects" it
takes down `thutapi` *and* `serp` immediately — both are on the default
cert — and takes down `auteur`, `eoc` and `mosaic` the moment their Let's
Encrypt certs lapse. The zone setting is not the bug; the broken DNS-01
token is. **Fix the token first, confirm real certs at every origin, and
only then move the zone to strict.**

## The cert blocker — Traefik's `CLOUDFLARE_DNS_API_TOKEN` cannot write DNS

Traefik's own ACME run is the evidence. Starting the container
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

### A correction to this round's own method

An earlier draft of this file cited
`GET /user/tokens/verify` returning `code 1000 Invalid API Token` as proof
the Traefik token was dead. **That test was the wrong endpoint and its
result was a false negative.** Cloudflare account-owned tokens (`cfat_`
prefix) verify at `/accounts/{account_id}/tokens/verify`; they return
`Invalid API Token` at the `/user/` endpoint even when perfectly healthy.
Confirmed the same afternoon: the operator's working token fails
`/user/tokens/verify` and passes the account endpoint, and then writes DNS
successfully.

The lego 401 above is unaffected and remains the sound evidence — it is
Traefik failing the actual write, not a verification proxy for it. The
conclusion stands; one of the two arguments for it did not.

**Fix:** install a token with `Zone → DNS → Edit` on `nryn.dev` into the
Traefik container's `CLOUDFLARE_DNS_API_TOKEN` and recreate it. That
issues the origin cert for `thutapi`, repairs `serp`, and restores renewal
for `auteur`/`eoc`/`mosaic`. It is **not** needed for the live URL, which
already works — it is needed before the zone can safely go to Full
(strict), and before the existing certs expire.

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

**PASS 2026-09-04.** Record created via the Cloudflare API:

```
$ curl -X POST ".../zones/$ZONE/dns_records" --data \
    '{"type":"A","name":"thutapi.nryn.dev","content":"167.233.247.107","proxied":true,"ttl":1}'
success: True
  thutapi.nryn.dev -> 167.233.247.107 proxied=True id=5b4f8e6dccd94e8bb0a1c4c4aa5462e6

$ dig +short thutapi.nryn.dev A @1.1.1.1
104.21.60.113
172.67.195.231
```

Proxied answers, matching `auteur`/`mosaic`/`eoc`/`serp` exactly.

### Check 3 — Let's Encrypt certificate issued

Run this **on the box**, against the origin — not from the public internet:

```bash
ssh foleyflow "echo | openssl s_client -servername thutapi.nryn.dev -connect 127.0.0.1:443 2>/dev/null \
  | openssl x509 -noout -subject -issuer -dates"
```

Expected: subject `CN=thutapi.nryn.dev` (or SAN containing it); issuer
`Let's Encrypt ... R3/R10/R11`; `notAfter` in the future.

**FAIL 2026-09-04.** The origin serves Traefik's built-in placeholder,
because DNS-01 could not write the challenge record:

```
$ echo | openssl s_client -servername thutapi.nryn.dev -connect 127.0.0.1:443 2>/dev/null \
    | openssl x509 -noout -subject -issuer
subject=CN=TRAEFIK DEFAULT CERT
issuer=CN=TRAEFIK DEFAULT CERT
```

Masked from the public by the Cloudflare edge cert and tolerated by the
zone's Full (non-strict) SSL mode — see the two sections above. This is
the one check still open, and it is infrastructure work, not Thutapi work.

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

**PASS 2026-09-04 15:29 UTC.**

```
HTTP/2 200
content-type: application/json
cf-cache-status: DYNAMIC
server: cloudflare
cf-ray: a35e07d26d497f45-MAA
alt-svc: h3=":443"; ma=86400

{"status":"ok","uptime_seconds":571,"version":"t1b-cfa8bd4"}
```

`cf-ray` and `server: cloudflare` confirm the proxy; the body confirms it
reached the container and not an error page. This is the end-to-end fact
T1b existed to establish, and T2/T14's "live app URL" dependency is now
satisfied.

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

> **Mis-scoped — found 2026-09-04.** This check cannot pass at T1b no
> matter what Cloudflare does. The T1 binary registers exactly one route,
> `s.mux.HandleFunc("GET /healthz", ...)` (`cmd/thutapi/main.go:117`).
> There is no static-file handler, so a file dropped in `/srv/thutapi/data`
> is not reachable over HTTP by anything. Serving it is **T3's** job —
> "Media on a Docker volume, served with `http.ServeContent` so Range
> requests work". Check 5 therefore depends on T3, not on T1, and belongs
> in T3's review round.
>
> This does not weaken the reason the check exists. The `source_audio`
> fetchability question is still the one environmental fact T13 cannot
> proceed without, and it is still worth answering early — it just cannot
> be answered until there is a route to answer it with. Once checks 2-4
> pass, the Cloudflare edge in front of `/media/...` is proven by check 4,
> and what remains for T3 is only the handler itself.

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