# Thutapi deployment — operator notes

These notes describe how the binary in this repo is shipped to the Hetzner
box `foleyflow` and served at **https://thutapi.nryn.dev**. They are written
for the operator (and for the next agent) — they are not user-facing docs.

The full environmental rationale is in `dev-diary/project.md §Deployment —
the box already does this` and the build plan is `dev-diary/PLAN.md §T1`.

## At a glance

| | |
|---|---|
| Host | Hetzner `foleyflow` (Ubuntu 26.04) |
| URL | https://thutapi.nryn.dev |
| Edge | Traefik v3, already on 80/443 with a `letsencrypt` certresolver |
| Routing | Docker labels on the container; no `docker-compose` project |
| Image base | `gcr.io/distroless/base-debian12:nonroot` |
| Listen | `0.0.0.0:8080` (`PORT` env, default 8080) |
| Persistence | Host bind mount at `/srv/thutapi/data` → container `/data` |

## Image

CI builds and publishes the image on every push to `main`
(`.github/workflows/image.yml`). It runs `go vet`, `go test -race` and a
`gofmt` check first, so a failing commit never reaches a tag the box could
pull. Two tags are published:

| Tag | Use |
|---|---|
| `ghcr.io/nrynss/thutapi:<short-sha>` | **Deploy this.** Names exactly one build, forever. |
| `ghcr.io/nrynss/thutapi:latest` | Convenience for humans. Moves. |

The package is public, so the box pulls anonymously — there is no
`docker login` on the host and no registry credential to manage.

```
docker pull ghcr.io/nrynss/thutapi:<short-sha>
```

To build locally instead (for a smoke test, or if CI is unavailable):

```
docker build --build-arg VERSION=$(git rev-parse --short HEAD) -t thutapi:local .
IMAGE=thutapi:local ./deploy/docker-run.sh
```

Shipping a workstation build straight to the box still works and is the
fallback if GHCR is unreachable:

```
docker save thutapi:local | ssh <box> 'docker load'
```

The Dockerfile is multi-stage: a `golang:1.27.1-bookworm` builder compiles
with `CGO_ENABLED=0`, `-trimpath`, `-ldflags="-s -w"`, and the distroless
stage ships the resulting static binary as the nonroot user.

## Starting the container

Use the script:

```
export GMI_API_KEY=...               # required
./deploy/docker-run.sh
```

The script starts the container in `--detach --restart unless-stopped`
mode, **attached to the `proxy` Docker network**, with the five Traefik
labels from `dev-diary/project.md §Deployment`:

```
traefik.enable=true
traefik.http.routers.thutapi.entrypoints=websecure
traefik.http.routers.thutapi.rule=Host(`thutapi.nryn.dev`)
traefik.http.routers.thutapi.tls.certresolver=letsencrypt
traefik.http.services.thutapi.loadbalancer.server.port=8080
```

TLS, cert renewal and the public-HTTPS edge are already provided by the
existing Traefik instance. The container does not need to publish a host
port — Traefik talks to it on 8080 by label.

**The `proxy` network is not optional.** `/opt/traefik/static.yml` sets the
docker provider to `exposedByDefault: false` with `network: proxy`. A
container that carries the labels but sits outside `proxy` is discovered
and then unroutable, which surfaces as a 404 or 502 rather than as an
obvious error. Override with `NETWORK=` only if Traefik's provider network
changes.

To stop and remove:

```
docker rm -f thutapi
```

## Environment variables

All configuration is read from environment (see `cmd/thutapi/main.go`
`parseConfig`; later tracks extend the struct).

| Variable | Required | Default | Notes |
|---|---|---|---|
| `PORT` | no | `8080` | Listen address inside the container. Must match `traefik.http.services.thutapi.loadbalancer.server.port`. |
| `DATA_DIR` | no | `/data` | SQLite file and generated media live here. Bind-mount a host path, owned by **uid 65532** (the distroless `nonroot` user). |
| `GMI_API_KEY` | **yes** | — | GMI Cloud inference key. **Never in the repo.** Passed via `docker run -e` from the operator shell. |
| `UPLOAD_TOKEN` | no | random | Bearer for the short-lived voice-sample upload path (T13). Regenerated per run. |

## Persistence — where `data/` lives on the box

The container's `/data` is a bind mount from `/srv/thutapi/data` on the
host. Layout:

```
/srv/thutapi/data/
├── thutapi.db            # SQLite (modernc.org/sqlite, pure Go)
└── media/                # generated images + audio, immutable, long max-age
    └── <book-id>/
        ├── pages/<n>.png
        ├── audio/<n>.mp3
        └── ref/<character>.png
```

Generated media accumulates — cap retention in T11 (hardening). The
publicly-fetchable URLs that GMI's `source_audio` needs (T8/T13) live under
`/media/...` and are served by the Go binary through `http.ServeContent`
so Range requests work (`<audio>` scrubbing depends on it).

## DNS

A single record, **proxied** through Cloudflare (matching `auteur`,
`mosaic`, `eoc`, `serp`):

| Type | Name | Target | Proxy |
|---|---|---|---|
| A | `thutapi.nryn.dev` | `${ORIGIN_IP}` | Proxied |

`${ORIGIN_IP}` is the box's real address, kept out of this repo on purpose.
It lives in the gitignored `.env` at the repo root (see `.env.example`),
and authoritatively in `~/work/hetzner/docs/foleyflow-server.md`.
Publishing it would defeat the orange cloud: with the origin address,
anyone can reach the box directly via `curl --resolve`, bypassing
Cloudflare's WAF, bot challenge and DDoS absorption — the host firewall
allows 80/443 from anywhere and Traefik routes purely on the Host header.

SSL mode on the zone is **`full`, not `strict`** — measured 2026-09-04,
`GET /zones/$ZONE/settings/ssl` returns `"value":"full"`. Flexible plus
Traefik's HTTPS redirect is an infinite redirect loop, so `full` is the
safe setting here.

Full encrypts to the origin without validating the origin certificate.
That was load-bearing until 2026-09-04, when `thutapi` and `serp` were
both serving Traefik's self-signed `CN=TRAEFIK DEFAULT CERT` because the
DNS-01 token could not write challenge records. The token has been
replaced and all five origins now hold real Let's Encrypt certificates, so
**strict is now safe** — it is simply not enabled, and nothing requires
it. If you do enable it, check every origin cert first: any hostname still
on the default cert returns 526 the instant strict is on.

### Certificates — DNS-01, and two certs not one

Traefik issues via **DNS-01** using a Cloudflare API token
(`CLOUDFLARE_DNS_API_TOKEN` in the Traefik container), not HTTP-01. Two
consequences that are easy to get wrong:

* Validation writes a `_acme-challenge` TXT record and never fetches the
  origin, so **proxying does not interfere with issuance** and the origin
  IP need not be publicly resolvable.
* There are **two** certificates on the path. The one a browser sees is
  Cloudflare's edge cert for the zone (issuer `O=Google Trust Services,
  CN=WE1`). Traefik's Let's Encrypt cert is the **origin** cert, the one
  Cloudflare validates under Full (strict). Checking for a Let's Encrypt
  issuer from the public internet will always fail; check it on the box:

```
ssh foleyflow "echo | openssl s_client -servername thutapi.nryn.dev \
  -connect 127.0.0.1:443 2>/dev/null | openssl x509 -noout -subject -issuer -dates"
```

If that returns `CN=TRAEFIK DEFAULT CERT`, issuance failed — check
`docker logs traefik | grep -i acme` and verify the Cloudflare token with
`curl https://api.cloudflare.com/client/v4/user/tokens/verify`.

## Cloudflare timeout constraint

A proxied request dies at **100 seconds with HTTP 524**. A book is ~10
image generations plus audio, so generation is **not** a blocking POST on
the public edge:

- Generation is *started* by a short POST that returns immediately with a
  job id, and *observed* over SSE. First byte is instant, so the 524 never
  arms.
- SSE responses set `Content-Type: text/event-stream`, `Cache-Control:
  no-cache` and `X-Accel-Buffering: no`, with a 15s heartbeat.
- Generated media is immutable, so it serves with a long `max-age` and
  Cloudflare's edge caches it for judges.

The full architecture rationale is in `dev-diary/project.md §Cloudflare —
the constraint that shapes the architecture`.

## Smoke verification

After every deploy, from the workstation or any host that can reach
`thutapi.nryn.dev`:

```
curl -fsS https://thutapi.nryn.dev/healthz
# -> {"status":"ok","uptime_seconds":N}
```

A 200 with a `cf-ray` header confirms the proxy, the cert and the routing
all the way through.

A local smoke run before pushing to the box:

```
docker build -t thutapi:local .
docker run --rm -p 18080:8080 -e PORT=8080 thutapi:local
# in another shell:
curl -fsS http://127.0.0.1:18080/healthz
# container exits on Ctrl-C; no dangling state
```

## Safety

- `GMI_API_KEY` is **never in the repo** and **never in the image**. It
  flows shell-env → `docker run -e` → container env. The repo is public
  for the whole judging period; sweep history before going public.
- Generated demo content is synthetic. No real PII, no real voice samples,
  no real voice clones.
- A public generate button is an open wallet at ~$0.01/image. T11 hardens
  this with a passcode or per-IP cap; until then, keep the live generate
  endpoint gated.
