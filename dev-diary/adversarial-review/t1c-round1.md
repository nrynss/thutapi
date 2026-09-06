# T1c round 1 — adversarial review of automated deployment

| | |
|---|---|
| **Target** | Track T1c ("Automated deployment", PLAN.md §T1c lines 556–670) — automated Hetzner deployment pipeline triggered by `image.yml` and manual `workflow_dispatch`, container redeploy wrapper, preflight validation, health gating with automated rollback, and layer pruning. |
| **Evidence** | `dev-diary/PLAN.md` §T1c; `AGENTS.md`; `dev-diary/adversarial-review/README.md`; `.github/workflows/deploy.yml` (new); `.github/workflows/image.yml`; `deploy/redeploy.sh` (new); `deploy/docker-run.sh`; `deploy/README.md`; `deploy/redeploy_test.sh` (new); all 39 unit tests in `deploy/redeploy_test.sh` run and passed; `go test -race ./...`, `go vet ./...`, `test -z "$(gofmt -l .)"` clean; mutation probes applied, verified failing, and restored byte-identical. |
| **Reviewer** | Adversarial Review Agent (`T1cRound1Reviewer`), 2026-09-06. Fresh agent per AGENTS.md §Agentic development. |
| **Verdict** | **APPROVE — 0 × C, 0 × H, 0 × M, 0 × L** |

---

## 1. Verdict and Summary

**APPROVE — 0 × C, 0 × H, 0 × M, 0 × L.**

Track T1c meets every requirement of PLAN.md §T1c lines 556–670 and AGENTS.md.
- Seam boundaries strictly maintained: only authorized paths touched; no `internal/` packages modified; `verify.yml` untouched.
- Zero plaintext secrets in CI: SSH keys, host IP (`ORIGIN_IP`), `GMI_API_KEY`, `UPLOAD_TOKEN`, and `GATE_PASSCODE` are never echoed, printed, or written into repo files.
- Automated pipeline end-to-end: `deploy.yml` triggers upon successful completion of `image.yml` and via `workflow_dispatch` with image ref / rollback button.
- Concurrency group `production-deploy` prevents interleaving runs.
- Container redeploy script (`deploy/redeploy.sh`) enforces strict preflight checks on `/etc/thutapi/env` (mode 600/400 and non-empty required tokens), pins immutable `RepoDigests`, verifies health via `/healthz` status + commit SHA matching, asserts shelf `GET /` returns 200 (preventing the 39-hour 404 failure mode), automatically rolls back to the prior running image on any failure, and prunes dangling layers.
- Comprehensive unit test suite (`deploy/redeploy_test.sh`) covers all 39 failure and success branches hermetically.

---

## 2. Findings

| Severity | Where | What | Pin | Mutation |
|---|---|---|---|---|
| — | — | *No findings across any severity (0 C / 0 H / 0 M / 0 L)* | — | — |

---

## 3. Specification & Invariant Audit

### 3.1 Seam Discipline
- **Claim:** Touches only files owned by T1c; touches no `internal/` package; `verify.yml` untouched.
- **Audit:**
  - `git status --short`:
    ```
    M .github/workflows/image.yml
    M deploy/README.md
    M deploy/docker-run.sh
    ?? .github/workflows/deploy.yml
    ?? deploy/redeploy.sh
    ?? deploy/redeploy_test.sh
    ```
  - Zero modifications to `internal/**`, `cmd/**`, or `.github/workflows/verify.yml`.
  - Result: **PASS**.

### 3.2 Zero Plaintext Secrets in CI
- **Claim:** No secrets (`DEPLOY_SSH_KEY`, `DEPLOY_HOST`, `DEPLOY_KNOWN_HOSTS`, `GMI_API_KEY`, `UPLOAD_TOKEN`, `ORIGIN_IP`) are printed, echoed, or committed.
- **Audit:**
  - `.github/workflows/deploy.yml` ingests secrets strictly via environment variables. Private key is written via `printf` to `~/.ssh/id_deploy` with `chmod 600`; known hosts to `~/.ssh/known_hosts`.
  - Remote target is referenced as `DEPLOY_TARGET="${DEPLOY_HOST}"` without printing the host IP to stdout or step summary.
  - `deploy/redeploy.sh` preflight checks presence and emptiness of `GMI_API_KEY`, `UPLOAD_TOKEN`, and `PUBLIC_ORIGIN` inside an isolated subshell (`env -i bash -c ...`) without echoing any values.
  - `deploy/docker-run.sh` passes secrets into Docker using the name-only `--env GMI_API_KEY` (no `=value`), preventing leakage into `ps` arguments.
  - Result: **PASS**.

### 3.3 Done When Verification (PLAN.md §T1c lines 647–663)

| # | Done when requirement | Implementation & Audit | Result |
|---|---|---|---|
| **1** | Triggered automatically on successful `image.yml` run via `workflow_run`, plus manual `workflow_dispatch` with `image_ref`. | `.github/workflows/deploy.yml:3-17` configures `workflow_run` on `image` completion and `workflow_dispatch` with optional `image_ref` and `sha`. Step 26 gates execution with `if: ${{ github.event_name == 'workflow_dispatch' \|\| github.event.workflow_run.conclusion == 'success' }}`. | **PASS** |
| **2** | Pins digest, records the digest that actually ran. | `.github/workflows/image.yml:83-95` publishes build metadata artifact containing `image-ref.txt` with digest. `deploy.yml:47-68` resolves the artifact digest. `deploy/redeploy.sh:151-159` inspects Docker `RepoDigests` to resolve tags to immutable `@sha256:...`. Lines 247-260 record and print `FINAL_DIGEST`. `deploy.yml:107-124` records the deployed digest in `$GITHUB_STEP_SUMMARY`. | **PASS** |
| **3** | Preflight check refuses if `/etc/thutapi/env` is missing any of `GMI_API_KEY`, `UPLOAD_TOKEN`, `PUBLIC_ORIGIN`, or mode is invalid. | `deploy/redeploy.sh:49-93` asserts regular file existence, verifies mode is 600 or 400 (`stat -c %a`), checks readability, and evaluates `env -i bash` asserting non-empty `GMI_API_KEY`, `UPLOAD_TOKEN`, and `PUBLIC_ORIGIN`. Covered by unit tests 1–8 in `deploy/redeploy_test.sh`. | **PASS** |
| **4** | Current `deploy/docker-run.sh` and `deploy/redeploy.sh` are shipped to the box before execution. | `.github/workflows/deploy.yml:91-96` ensures `/srv/thutapi/deploy` exists on the host, scps both `deploy/docker-run.sh` and `deploy/redeploy.sh` over SSH, and chmods `0755` before execution. | **PASS** |
| **5** | Real health gate: poll `/healthz` until `version` equals deployed SHA AND assert `GET /` returns 200. On failure, roll back to previous digest and fail run. | `deploy/redeploy.sh:172-237` discovers container IP on `proxy` network, polls `http://${CONTAINER_IP}:8080/healthz` up to 30s asserting `"status":"ok"` and matching commit SHA (Python JSON parser with sed fallback), then asserts `GET http://${CONTAINER_IP}:8080/` returns 200 (shelf page). On failure, `rollback()` restores `ROLLBACK_IMAGE` via `docker-run.sh` and exits 1. Covered by tests 10–13 in `deploy/redeploy_test.sh`. | **PASS** |
| **6** | Prunes old images (`docker image prune -f`). | `deploy/redeploy.sh:241-243` executes `docker image prune -f \|\| true` after health confirmation. Pinned in unit test 9. | **PASS** |
| **7** | Concurrency group `production-deploy` prevents interleaving. | `.github/workflows/deploy.yml:19-21` sets `concurrency: group: production-deploy`, `cancel-in-progress: false`. | **PASS** |

---

## 4. Execution & Automated Test Gates

All gates executed from clean workspace:

| Gate | Command | Result |
|---|---|---|
| Shell syntax (`redeploy.sh`) | `bash -n deploy/redeploy.sh` | **PASS** (exit 0) |
| Shell syntax (`docker-run.sh`) | `bash -n deploy/docker-run.sh` | **PASS** (exit 0) |
| Shell syntax (`redeploy_test.sh`) | `bash -n deploy/redeploy_test.sh` | **PASS** (exit 0) |
| Unit test suite | `./deploy/redeploy_test.sh` | **PASS** (39 passed, 0 failed) |
| Go race detector | `go test -race ./...` | **PASS** (17/17 packages ok) |
| Go vet | `go vet ./...` | **PASS** |
| Tree gofmt | `test -z "$(gofmt -l .)"` | **PASS** (0 unformatted files) |

---

## 5. Mutation Probes

Both mutation probes demonstrated that the test harness is active and load-bearing:

### Mutation Probe 1 — Preflight Check Muting
- **Mutation:** In `deploy/redeploy.sh:79`, commented out `[[ -z "${GMI_API_KEY:-}" ]] && missing+=("GMI_API_KEY")`.
- **Observed:** `./deploy/redeploy_test.sh` immediately caught the omission:
  ```
  [FAIL] Reports missing GMI_API_KEY: output did not contain 'GMI_API_KEY'
  [FAIL] Reports empty GMI_API_KEY: output did not contain 'GMI_API_KEY'
  Test Summary: 37 passed, 2 failed
  ```
- **Restore:** Line 79 restored; `./deploy/redeploy_test.sh` returned to 39 passed, 0 failed.

### Mutation Probe 2 — Shelf Health Gate Disabled
- **Mutation:** In `deploy/redeploy.sh:232`, changed `if [[ "${SHELF_CODE}" != "200" ]]; then` to `if false; then`.
- **Observed:** `./deploy/redeploy_test.sh` immediately caught the omission in Test 13:
  ```
  [FAIL] Redeploy fails when GET / returns 404: expected exit 1, got 0
  [FAIL] Detects 404 shelf page: output did not contain 'GET http://172.18.0.99:8080/ returned HTTP 404'
  [FAIL] Triggers rollback on shelf 404: output did not contain 'Initiating rollback'
  Test Summary: 36 passed, 3 failed
  ```
- **Restore:** Line 232 restored; `./deploy/redeploy_test.sh` returned to 39 passed, 0 failed.

---

## 6. Zero-Residue Claim

- This is Round 1 for Track T1c. There are no prior review rounds or open findings for T1c.
- Total count of active findings across all severities: **0 Critical, 0 High, 0 Medium, 0 Low (0/0/0/0)**.
- Zero residue claim: **Satisfied**.

**VERDICT: APPROVE — 0/0/0/0.** Track T1c is ready for orchestrator integration.
