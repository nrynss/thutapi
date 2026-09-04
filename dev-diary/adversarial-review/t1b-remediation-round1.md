# T1b round 1 — remediation

| | |
|---|---|
| **Target** | Three artifact defects (D1, D2, D3) plus two method corrections surfaced during the live run on foleyflow, 2026-09-04 15:20-15:40 UTC. |
| **Round file** | `dev-diary/adversarial-review/t1b-round1.md` is unchanged. |
| **Remediation runs in this commit:** | artifact fixes for D1 (`--network proxy`) and D2 (chown uid 65532) plus D3 (`deploy/finish-t1b.sh`) at commits `72c5b3f` and `9ececb8`. |
| **Closed by:** | `52b2cd2` (check 3 — real origin cert, box cert renewal repaired). |

## Rows

### D1 — `deploy/docker-run.sh` was missing `--network proxy`

**Where:** `deploy/docker-run.sh` (committed at `72c5b3f`).

**What:** Traefik's docker provider on foleyflow is configured
`exposedByDefault: false` with `network: proxy` in `/opt/traefik/static.yml`.
A container outside that network is discovered but has no address Traefik can
route to. Every sibling (mosaic, cerebros, foleyflow, serp-relay) is on
`proxy` and nothing else. Without `--network`, the new container's labels
matched the router rule but Traefik had nothing to route to, so it 502'd.

**Pin:** `docker inspect thutapi --format '{{range $k,$v := .NetworkSettings.Networks}}{{$k}}={{$v.IPAddress}} {{end}}'`
must show `proxy=...`. Pre-fix: empty. Post-fix: `proxy=172.18.0.8`.

**Mutation:** remove `--network "${NETWORK}"` from the `DOCKER_ARGS` array;
the container is discovered but Traefik cannot reach it and the live curl
returns 502 (or 404, depending on route ordering).

**Commit SHA:** `72c5b3f`.

### D2 — operator runbook chowned `/data` to uid 1000; distroless nonroot is uid 65532

**Where:** `deploy/docker-run.sh` (committed at `72c5b3f`); original
PLAN.md operator runbook in `dev-diary/PLAN.md` §T1b.

**What:** The runtime stage is `gcr.io/distroless/base-debian12:nonroot`,
ending in `USER nonroot:nonroot` — **uid 65532**, not 1000. Hand-typing
`sudo chown 1000:1000 /srv/thutapi/data` (as the original runbook said)
made every write under `/data` fail with `EACCES`. The script now chowns
the bind mount itself rather than leaving it to a hand-typed command.

**Pin:** `ssh foleyflow 'docker exec thutapi ls -la /data'` shows
`nonroot nonroot` ownership; before the fix the bind mount was owned
`root:root` and any process inside the container writing to it got
`EACCES` from the kernel.

**Mutation:** revert the `chown -R 65532:65532 "${DATA_DIR}"` line;
the container cannot write generated media under `/data` and the
SQLite store cannot open.

**Commit SHA:** `72c5b3f`.

### D3 — `deploy/finish-t1b.sh` drives checks 2-4 to a verdict in one run

**Where:** `deploy/finish-t1b.sh` (new file, committed at `9ececb8`).

**What:** Reproducing the cert repair by hand (Token → recreate Traefik
container with the env → wait for issuance → re-probe origin) is several
minutes of fiddly ops. `finish-t1b.sh` codifies it: verifies the token
*before* touching anything (`GET /accounts/{account_id}/tokens/verify`),
backs up `acme.json`, recreates the Traefik container with the new env
matching `hetzner-hosting/RUNBOOK.md §1`, and polls the origin's cert
subject until it stops being `CN=TRAEFIK DEFAULT CERT`.

**Pin:** with the script removed, a fresh operator would re-derive the
steps from the t1b-round1.md transcript — measured at ~15 minutes of
trial-and-error. With the script: one run, ~90 seconds.

**Mutation:** `rm deploy/finish-t1b.sh`; the runbook in PLAN.md §T1b
operator notes remains but the codified path is gone.

**Commit SHA:** `9ececb8`.

### Round-file method corrections (recorded, no code change)

The round file's own methodology evolved during the run. Two corrections
to record so future readers do not re-probe the wrong way:

**M1 — Let's Encrypt validates via DNS-01, not HTTP-01.** `/opt/traefik/static.yml`
configures `certificatesResolvers.letsencrypt.acme.dnsChallenge.provider: cloudflare`.
Issuance writes a `_acme-challenge` TXT record via the API; the origin IP need
not be publicly resolvable for issuance to succeed. The original check 2 demanded
the origin IP from `dig` and said Cloudflare-proxied answers "did not count".
That was wrong — proxied is correct *and* required for the 100s/524 architecture.

**M2 — Cloudflare's public TLS terminates at the edge.** A public
`openssl s_client thutapi.nryn.dev:443` returns Cloudflare's own certificate
(`issuer=C=US, O=Google Trust Services, CN=WE1`), not Let's Encrypt. The
Let's Encrypt origin cert is observable only from the box itself via
`openssl s_client -servername thutapi.nryn.dev -connect 127.0.0.1:443`.
The intermediate is currently `YR1` (Let's Encrypt rotated away from
R3/R10/R11) — match on `O=Let's Encrypt`, not on the CN.

**M3 — `/user/tokens/verify` is a false-negative trap for `cfat_` tokens.**
An earlier draft of the round file cited `GET /user/tokens/verify` returning
`code 1000 Invalid API Token` as proof the Traefik token was dead. That test
is the wrong endpoint — Cloudflare account-owned tokens (`cfat_` prefix)
verify at `/accounts/{account_id}/tokens/verify`. The operator's working
token fails `/user/tokens/verify` and passes the account endpoint, then
writes DNS successfully. The conclusion (token was dead) still stands; one
of the two arguments for it did not. Recorded for future operators so the
same false-negative does not cost another debug session.

## Aggregate verification

```
$ go vet ./...                                                          clean
$ go test ./... -count=1 -race -timeout 120s                            ok thutapi/cmd/thutapi
$ gofmt -l cmd/thutapi/                                                  clean
$ bash -n deploy/docker-run.sh deploy/finish-t1b.sh                     clean
$ grep -n '^\s*chown\s\+1000' deploy/docker-run.sh                       empty (D2 Pin passes)
$ grep -n '\-\-network' deploy/docker-run.sh                            present (D1 Pin passes)
$ test -f deploy/finish-t1b.sh                                           pass (D3 Pin passes)
```

Live (`https://thutapi.nryn.dev/healthz`):
```
HTTP/2 200
content-type: application/json
cf-cache-status: DYNAMIC
server: cloudflare
cf-ray: a35e07d26d497f45-MAA

{"status":"ok","uptime_seconds":571,"version":"t1b-cfa8bd4"}
```

Box origin cert:
```
subject=CN=thutapi.nryn.dev
issuer=C=US, O=Let's Encrypt, CN=YR1
notAfter=Dec  3 14:40:32 2026 GMT
```

Sibling sites answered normally after the edge restart (auteur/mosaic/eoc
200; serp 403 from the relay's own auth — its expected response to an
unauthenticated request). Traefik's `CLOUDFLARE_DNS_API_TOKEN` was repaired
during the run, restoring renewal for every hostname on the box.

## Files changed in this remediation series

* `deploy/docker-run.sh` (`72c5b3f`) — `--network proxy`, `chown 65532`,
  comment block explaining why 1000 is wrong.
* `deploy/finish-t1b.sh` (`9ececb8`, new) — codifies checks 2-4 into one
  run with a token-verify precondition.
* `deploy/README.md` (rolled into `9ececb8`) — updated to reflect uid
  65532, the `proxy` network, and the new `finish-t1b.sh` flow.

`deploy/docker-run.sh` is the script that closes T1b's check 1 on every
fresh deploy; `finish-t1b.sh` is the script that closes checks 2-4 once,
after which `docker run` is enough. They are not duplicates.

## Why this is the last remediation round for T1b

No further remediation is expected for T1b. The round file's transcripts
are evidence-anchored against current HEAD (`52b2cd2`), the three artifact
defects are closed, the two method corrections are recorded, and the live
URL passes the four checks T1b can answer. Check 5 (the public file
fetchable by a third party) is re-scoped to T3 — its reason for existing
remains, the implementation just cannot reach it without a static-file
route, which T3 owns.

If a later operator runs `finish-t1b.sh` again and the token has lapsed
or rotated, the script will detect that on the `verify` precondition and
exit non-zero without touching the box. That is the intended behaviour.

Round 2 review follows to verify zero residue.