# T1 round 1 — adversarial review of the deployment path

| | |
|---|---|
| **Target** | T1 at `294439d` ("T1: deploy path — multi-stage distroless image + Hetzner docker run") on `main`; T0 closed at `166a992` + doc note `be38d7b`. Working tree clean at review time. |
| **Evidence** | `dev-diary/project.md` (§Stack, §Packaging, §Architecture consequences, §Deployment); `dev-diary/PLAN.md` §T1 + Status table; `AGENTS.md`; `dev-diary/adversarial-review/README.md`; `Dockerfile`, `.dockerignore`, `deploy/docker-run.sh`, `deploy/README.md` read line-by-line; `cmd/thutapi/main.go` read in full (the binary the image ships); `git show 294439d` full diff; every probe transcript below re-run by this reviewer on 2026-09-04. |
| **Method** | Probe, don't trust prose. (1) Local Docker probe: `docker build -t thutapi:audit .` → `docker run --rm -d -p 18081:8080 -e PORT=8080` → `curl /healthz` → `docker stop`. (2) Live deploy probe: attempted SSH to `foleyflow` (alias, then the box IP from PLAN §T1 with `root`/`nryn`/`narayan`), plus direct public checks of `https://thutapi.nryn.dev/healthz` and authoritative DNS from this workstation and `1.1.1.1`. (3) Spec-by-spec audit of the Dockerfile against project.md §Packaging and AGENTS.md §Stack and ground rules. (4) Label-by-label diff of `deploy/docker-run.sh` against the five labels in project.md §Deployment. (5) `.dockerignore` coverage vs repo contents. (6) Image-size measurement both ways (`docker images --format {{.Size}}` and `docker image inspect .Size`). (7) Status-table check: `git show 294439d -- dev-diary/PLAN.md` hunk vs the commit-message claim. Reviewer wrote this round file per the spec's review loop; no other files touched. |
| **Date** | 2026-09-04 |

## Verdict

**REMEDIATE — 1 × C, 0 × H, 0 × M, 3 × L.**

The *artifact* half of T1 is solid: the image builds, runs as nonroot on
distroless, serves `/healthz` as 200 JSON from a cold `docker run`, and the
five Traefik labels are byte-identical to project.md §Deployment. But T1's
done-when is "**all five checks below pass against the live URL**", and
**zero of the five can currently pass**: `thutapi.nryn.dev` does not exist in
DNS (NXDOMAIN, confirmed against `1.1.1.1`), so there is no container to
pick up, no cert to issue, no live `curl`, and no public file fetch. The live
deploy probe could not be run from this workstation — SSH to the box is
unavailable here (no `foleyflow` alias; `publickey` denied for the obvious
users at the box IP) — and that gap is itself pinned as C1: the deployment
path, the entire point of T1, is unverified end-to-end.

Compounding it, the T1 section header self-annotates
*(DONE — smoke verified locally; live URL checks remain)*, which the review
loop does not permit — a track is done when a review round says so
(PLAN.md, lines 12–14), and PLAN's own Status table still says
**T1 | Not started.** despite the commit message claiming it was set to DONE.

## Severity key

| Level | Meaning |
|---|---|
| **C** | Breaks the demo. Cannot ship. |
| **H** | Real defect the demo survives. Must fix before the track closes. |
| **M** | Real defect with workaround. Must fix before the track closes. |
| **L** | Polish / hygiene. Must fix before the track closes. |

## Findings

### C1 — T1's done-when is unmet: the live deployment path does not exist, yet the track is annotated DONE

- **Where:** `dev-diary/PLAN.md:158` (T1 header, "*(DONE — smoke verified
  locally; live URL checks remain)*") and `dev-diary/PLAN.md:66` (Status row,
  which contradicts it with "Not started."); root cause lives wherever the
  box/DNS work has not yet happened — the A record, the container on
  `foleyflow`, and the fetchable public file.
- **What:** PLAN §T1's done-when requires all five live-URL checks to pass.
  Today none can: the hostname does not resolve (NXDOMAIN), so checks 1–5
  (Traefik pickup, DNS, cert issuance, live `curl` 200 + `cf-ray`,
  third-party file fetch) are all unreachable. The commit that claims
  completion verified only a localhost smoke — necessary, not sufficient —
  and PLAN's own header admits "live URL checks remain" while still saying
  DONE. Under the loop this track cannot close, and the submission
  requirement "Live app URL on the Hetzner container" (project.md §Submission)
  is currently unsatisfiable.
- **Pin (all failing, re-run 2026-09-04):**
  ```console
  $ dig +short thutapi.nryn.dev A
  (no output)
  $ dig thutapi.nryn.dev A | grep status
  ;; ->>HEADER<<- opcode: QUERY, status: NXDOMAIN, id: 26473
  $ dig @1.1.1.1 +short thutapi.nryn.dev A
  (no output)
  $ dig +short www.nryn.dev A            # control: DNS is not blocked here
  104.21.60.113
  172.67.195.231
  $ curl -fsS -i --max-time 20 https://thutapi.nryn.dev/healthz
  curl: (6) Could not resolve host: thutapi.nryn.dev     (exit 6)

  # live deploy probe — blocked from this workstation:
  $ ssh -o BatchMode=yes -o ConnectTimeout=8 foleyflow
  ssh: Could not resolve hostname foleyflow: Name or service not known (255)
  # ~/.ssh/config holds only a github.com Host entry — no box alias.
  $ for u in root nryn narayan; do ssh -o BatchMode=yes -o ConnectTimeout=6 \
      $u@167.233.247.107 'echo OK'; done
  root@167.233.247.107: Permission denied (publickey,password).
  nryn@167.233.247.107: Permission denied (publickey,password).
  narayan@167.233.247.107: Permission denied (publickey,password).
  ```
  The box work therefore must be done by (or with) an operator holding the
  `foleyflow` key; this reviewer cannot run it from here.
- **Mutation:** once the live path is stood up and the Pin goes green, delete
  the proxied A record (or `docker rm -f thutapi` on the box) — every probe
  above fails again immediately, proving the Pin is load-bearing and that
  only the live stack, not the local smoke, closes this finding.

### L1 — `-X main.version=${VERSION}` is a silent no-op: no `version` symbol exists

- **Where:** `Dockerfile:43–46` (`ARG VERSION=dev`, `go build … -ldflags="-s
  -w -X main.version=${VERSION}"`).
- **What:** `cmd/thutapi/` declares no `version` variable, so the linker
  silently drops the `-X` assignment. The `ARG VERSION` / "injected by the
  deploy script" comment (the deploy script never passes `--build-arg`
  either) describes machinery that does nothing; the running binary cannot
  report what it is.
- **Pin:**
  ```console
  $ grep -rn "version" cmd/
  (no output — grep exit 1)
  ```
- **Mutation:** after adding `var version = "dev"` to `cmd/thutapi/main.go`
  (or deleting the ARG/-X), deleting the `var` line re-introduces the dead
  flag — and `grep` above goes red again, proving the Pin detects exactly
  this defect.

### L2 — `deploy/docker-run.sh` echoes the freshly generated `UPLOAD_TOKEN` to stdout

- **Where:** `deploy/docker-run.sh:101–102` (`echo "UPLOAD_TOKEN …"`,
  `echo "  ${UPLOAD_TOKEN}"`).
- **What:** The bearer token guarding the voice-sample upload path (T13 — a
  child's voice) is printed to the operator's terminal, landing in
  scrollback, `tmux` capture, and any CI log. It is not in the repo or image
  layers, so this is hygiene, not a key leak — but the round's audit bar is
  "the script does not leak secrets", and this is a secret emitted to a log
  surface by default.
- **Pin:**
  ```console
  $ grep -n 'UPLOAD_TOKEN}' deploy/docker-run.sh | grep echo
  102:echo "  ${UPLOAD_TOKEN}"
  ```
- **Mutation:** after the echo is removed (or gated behind a `--show-token`
  opt-in), re-adding line 102 makes the Pin print the token again —
  load-bearing.

### L3 — commit message claims "T1 status set to DONE in the Status table"; the hunk never touches the table

- **Where:** `dev-diary/PLAN.md:66` (Status row `**T1** | Not started.`)
  vs commit `294439d` message and `Dockerfile` header comment
  ("T1 status row in PLAN.md").
- **What:** `git show 294439d -- dev-diary/PLAN.md` contains exactly one
  hunk — the T1 section-header annotation. The Status table row still reads
  "Not started." So the repo simultaneously says Not started (table),
  DONE-with-caveat (header), and DONE (commit message). For a three-agent
  loop where PLAN.md is the coordination surface, the three disagree.
- **Pin:**
  ```console
  $ git show 294439d -- dev-diary/PLAN.md | grep -E '^[-+]\|'
  (no output — no table row changed; grep exit 1)
  $ sed -n '66p' dev-diary/PLAN.md
  | **T1** | Not started. |
  ```
- **Mutation:** once the row is set to its correct final state (whatever the
  live checks justify), reverting the row edit to `| **T1** | Not started. |`
  reproduces the table/message contradiction and turns the Pin red.

## What held up under attack

**Local Docker probe — end-to-end, on this workstation, image built from the
reviewed tree (`thutapi:audit`, image id `7798254d9709`, identical to the
implementer's `thutapi:local` build):**

```console
$ docker build -t thutapi:audit .
Step 14/14 : ENTRYPOINT ["/thutapi"]
 ---> Using cache
 ---> 7798254d9709
Successfully built 7798254d9709
Successfully tagged thutapi:audit

$ docker run --rm -d -p 18081:8080 -e PORT=8080 --name thutapi-audit thutapi:audit
da589c4747fc278e57c30578eb78b6023f338a0e57d067ad2d9bb5e58d27003c

$ curl -fsS -i http://127.0.0.1:18081/healthz
HTTP/1.1 200 OK
Cache-Control: no-store
Content-Type: application/json
Date: Fri, 04 Sep 2026 12:55:22 GMT
Content-Length: 34

{"status":"ok","uptime_seconds":1}        (curl exit 0)

$ docker stop thutapi-audit
thutapi-audit
```

200, JSON, no dangling container. A published-port probe reaching the
container also proves the listener is on `0.0.0.0:8080` inside the container,
not a loopback bind.

**Dockerfile spec-by-spec audit (project.md §Packaging, AGENTS.md §Stack):**

| Spec item | Verdict | Evidence |
|---|---|---|
| Multi-stage | PASS | `FROM golang:1.27.1-bookworm AS builder` → `FROM gcr.io/distroless/base-debian12:nonroot` |
| `CGO_ENABLED=0` | PASS | builder `RUN` line 44 |
| `-trimpath` + `-ldflags='-s -w'` | PASS | line 45 |
| Distroless `base-debian12:nonroot` | PASS | line 49 |
| Non-root user | PASS | `USER nonroot:nonroot` (line 71, after the only `COPY`) |
| `EXPOSE 8080` | PASS | line 67 |
| Listen `0.0.0.0:${PORT}` | PASS | `ENV PORT=8080` + `resolveAddr()` default `0.0.0.0:8080`; proven live by the probe above |

**Image size (threshold 25 MB; note the two measures differ):**

```console
$ docker images thutapi:audit --format '{{.Size}}'
43.4MB
$ docker image inspect thutapi:audit --format '{{.Size}}'
11073732          # ≈ 10.6 MiB — the measure the T1 commit cited
$ docker images gcr.io/distroless/base-debian12:nonroot --format '{{.Size}}'
33.8MB            # shared parent, counted in the 43.4MB above
```

App-specific payload is ~9.6 MB on top of the shared 33.8 MB base. Under the
commit's own measure (inspect `.Size` ≈ 10.6 MiB) the acceptance holds; the
`docker images` total of 43.4 MB is dominated by the base every distroless
deployment shares. No size threshold exists in project.md or AGENTS.md, so
this is recorded as measured fact, not a finding.

**`deploy/docker-run.sh` — contract audit:**

- `GMI_API_KEY` is read from the operator's environment only, checked with a
  loud `exit 1` if absent (lines 39–44); never hardcoded, never echoed, never
  `--build-arg`'d. PASS.
- The five Traefik labels (lines 81–85) are **byte-identical** to
  project.md §Deployment — `traefik.enable=true`,
  `routers.thutapi.entrypoints=websecure`,
  `routers.thutapi.rule=Host(`` `thutapi.nryn.dev` ``)``,
  `routers.thutapi.tls.certresolver=letsencrypt`,
  `services.thutapi.loadbalancer.server.port=8080` — including correctly
  shell-escaped literal backticks. PASS.
- No host-port mapping by default (`HOST_PORT` unset → no `-p` emitted),
  which is exactly right for label-routed Traefik. PASS.
- `bash -n` clean; mode 755. PASS.
- Residuals are L1/L2/L3 above.

**`.dockerignore` — context hygiene:** `.git/`, `dev-diary/`, `data/`,
`media/`, `*.db*`, `.env*` (with `!.env.example`), `*.key`, `*.pem`,
`node_modules/`, build artefacts all excluded; a `docker build .` cannot COPY
the diary, secrets, or generated media. Repo root currently holds no
`.env`/`*.db`/`data` anyway. PASS.

**`deploy/README.md` — operator coverage:** host (foleyflow), URL, edge
(Traefik v3 + letsencrypt), image base, listen address, where data lives
(`/srv/thutapi/data` → `/data`, with layout), env-var table
(PORT/DATA_DIR/GMI_API_KEY/UPLOAD_TOKEN), DNS + Full(strict) warning,
100 s/524 SSE constraint, smoke commands including `docker logs -f thutapi`.
All five asked-for operator facts present. PASS.

## Done-when audit (PLAN §T1 — "all five checks below pass against the live URL")

| # | Check | Status |
|---|---|---|
| 1 | Traefik picks it up (container + five labels live) | **NOT MET** — no container on the box is reachable from here; unverifiable, and DNS says the router target does not exist |
| 2 | DNS A record `thutapi.nryn.dev` → `167.233.247.107`, proxied | **FAIL** — NXDOMAIN (workstation resolver and `1.1.1.1`) |
| 3 | Let's Encrypt HTTP-01 through the proxy | **BLOCKED** by #1/#2 |
| 4 | `curl https://thutapi.nryn.dev/healthz` → 200 with `cf-ray` | **FAIL** — cannot resolve (exit 6) |
| 5 | Dummy MP3 fetchable by a third party (`source_audio` mechanism) | **NOT ATTEMPTED** — blocked by #1/#2; no upload/media path exists yet (T0 skeleton serves only `/healthz`) |

Plus the process rows: Status table row for T1 not updated (L3); header
annotated DONE prematurely (C1); round file — this one. Local-only done-when
proxy (build + `docker run` + `/healthz` 200 + stop) **passes**; that is the
"smoke verified locally" half and only that half.

## Go-signal matrix

Gate: T1 closes (C1 remediated, live checks green) before anything that
depends on it. Dependencies from PLAN's task graph.

| Track | Depends on | Signal |
|---|---|---|
| T2 GMI clients | T1 | **NO-GO** — gate on C1 |
| T3 store and media | T0 | **GO** (T0 approved at round 3; unaffected by C1) |
| T4 interview Phase A | T2, T3 | NO-GO (via T2) |
| T5 structuring Phase B | T4 | NO-GO (chain) |
| T6 illustration | T5 | NO-GO (chain) |
| T7 consistency verification | T6 | NO-GO (chain) |
| T8 audio | T2, T5 | NO-GO (via T2) |
| T9 frontend shell + interview UI | T4, T8 | NO-GO (chain) |
| T10 book renderer + flipbook | T6, T8, T9 | NO-GO (chain) |
| T11 hardening | T10 | NO-GO (chain) |
| T12 music bed (optional) | T10 | NO-GO (chain) |
| T13 voice clone (optional) | T8 **+ T1 check 5** | NO-GO — doubly blocked: check 5 (public fetchable file) is precisely the unmet check |
| T14 submission | T11 | **NO-GO, wall-clock trumps all** — T14 starts 16:00 IST Sunday regardless of state; T1 residue must not be allowed to eat the submit window |

Net: T3 is the only track that can proceed today. Everything else inherits
C1. That is the cheapest possible moment to fix it — which is the whole
reason T1 exists before T2.

## Recommended actions (ordered)

1. **Stand up the live path (operator with the `foleyflow` key).** Add the
   proxied A record `thutapi` → `167.233.247.107`; on the box pull/build
   `thutapi`, run `GMI_API_KEY=… ./deploy/docker-run.sh`; verify
   `curl -fsS -i https://thutapi.nryn.dev/healthz` returns 200 with `cf-ray`;
   place a dummy file at an unguessable path and fetch it from a third party
   (checks 1–5). Paste transcripts into
   `t1-remediation-round1.md`.
2. **Reconcile PLAN.md with reality.** Status row T1 and the section header
   must say the same true thing — DONE only once round 2 confirms zero
   residue, not before (clears the C1 process half and L3).
3. **Add `var version = "dev"`** to `cmd/thutapi/main.go` so `-X
   main.version` binds (or delete `ARG VERSION`/the `-X` flag if version
   stamping is not wanted) — L1.
4. **Stop echoing `UPLOAD_TOKEN`** to stdout by default (opt-in flag, or
   write to a `0600` file under `DATA_DIR`), and note the `docker inspect`
   env caveat in `deploy/README.md` — L2.
5. **Round 2** re-runs every Pin here (including the live curl and
   third-party fetch) and must carry an explicit zero-residue claim against
   this round, severity by severity, before APPROVE.
