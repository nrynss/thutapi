# Option A (Prewarm Image Baking) & Live Bug 4 (Shelf Cache-Busting) — Round 1 Adversarial Review

| | |
|---|---|
| **Target** | Option A: Baking prewarm fixtures into the Docker image; Live Bug Report 4: Static assets cache-busting and shelf script cache-busting. Working tree on `main`. |
| **Reviewer** | Adversarial Review Agent (`gpt-5.6-terra`), 2026-09-06. Fresh agent; not the implementer. |
| **Evidence read** | `AGENTS.md`; `dev-diary/PLAN.md` (Live Bug Report 4 & Prewarm Option A decision); `Dockerfile`; `.dockerignore`; `cmd/thutapi/main.go`; `cmd/thutapi/main_test.go`; `internal/web/templates/shelf.html`; `internal/web/web_test.go`; `deploy/docker-run.sh`; `deploy/redeploy.sh`; `deploy/redeploy_test.sh`. |
| **Verification Gates** | `go test -race ./...` (PASS); `go vet ./...` (PASS); `test -z "$(gofmt -l .)"` (PASS); `deploy/redeploy_test.sh` (39/39 PASS); `docker build` and container cold boot runtime probe (PASS); `git diff --check` on code files (PASS); `git diff --check dev-diary/PLAN.md` (FAIL: whitespace error `blank-at-eof`). |
| **Verdict** | **REMEDIATE — 0 × C, 0 × H, 0 × M, 1 × L** |

---

## Executive Summary & Diff Analysis

The review target addresses two production issues identified during live navigation (`https://thutapi.nryn.dev/`):
1. **Option A (Baking prewarm fixtures into Docker image):**
   - `.dockerignore`: Changed `data/` ignore rule to `data/*` with `!data/prewarm` exception, ensuring prewarm fixtures are included in the Docker build context while transient SQLite databases (`*.db*`) and runtime files remain excluded.
   - `Dockerfile`:
     - Stage 1 (builder): Adds `RUN chmod -R a+rX /src/data/prewarm` ensuring all fixture files and directories have universal read and traverse permissions before copy.
     - Stage 2 (runtime): Adds `COPY --from=builder /src/data/prewarm /prewarm` and sets `ENV PREWARM_DIR=/prewarm`. Container drops privileges to `USER nonroot:nonroot` (uid 65532).
     - Hermetic startup: Container boots cleanly without host bind-mount synchronization, importing prewarmed book fixtures directly into `/data` on startup.
2. **Live Bug Report 4 (Static assets cache-busting):**
   - `cmd/thutapi/main.go`: `staticAssets()` sets `Cache-Control: no-cache, must-revalidate` on all served static assets, preventing Cloudflare edge caching (`cf-cache-status: HIT`) and browser caching of stale scripts across deployments while preserving 404 filtering for internal developer files.
   - `internal/web/templates/shelf.html`: Updates client Preact bootstrap script to `<script type="module" src="/static/app.js?v=2"></script>`, immediately busting stale edge and browser caches.
   - `internal/web/web_test.go`: Asserts `<script type="module" src="/static/app.js?v=2"></script>` in rendered shelf HTML.
   - `cmd/thutapi/main_test.go`: Added `TestStaticAssets_CacheControlAndFiltering`, `TestDockerfile_OptionAPrewarmAndPermissions`, and `TestParseConfig_PrewarmDir`.

All core functionality, permissions, and tests are sound. A single cosmetic finding (L1) was discovered in `dev-diary/PLAN.md` where trailing blank lines cause `git diff --check` to fail with whitespace error `blank-at-eof`.

---

## Severity Counts

| Level | Description | Count |
|---|---|---|
| **C** (Critical) | Breaks the demo | **0** |
| **H** (High) | Real defect demo survives | **0** |
| **M** (Medium) | Real defect with workaround | **0** |
| **L** (Low) | Polish / hygiene / whitespace | **1** |

---

## Findings Table

| # | Severity | Where | What | Pin (failing probe or test) | Mutation (what breaks if reverted) |
|---|---|---|---|---|---|
| **L1** | **L** | `dev-diary/PLAN.md:3138` | Extraneous blank line at end of file causes `git diff --check` to fail with exit code 2 (whitespace error `new blank line at EOF`). | Shell probe: `git diff --check dev-diary/PLAN.md`<br>Output: `dev-diary/PLAN.md:3138: new blank line at EOF.` (exit code 2) | Delete trailing blank line at `dev-diary/PLAN.md:3138` -> `git diff --check` exits 0. Re-adding the trailing blank line causes `git diff --check` to fail with exit 2. |

### Orchestrator Fast-Path Assessment (AGENTS.md §The one exemption)

Finding **L1** strictly meets all four conditions for the orchestrator fast-path:
1. **Severity is L:** Purely formatting/whitespace hygiene.
2. **Cannot alter behaviour:** Markdown text file (`PLAN.md`), prose only; no logic, branch, value, or code signature.
3. **Recorded by name and file:** Recorded here as **L1** in `dev-diary/PLAN.md:3138`.
4. **Inarguable triviality:** Eliminating an extraneous trailing blank line to satisfy `git diff --check`.

**Orchestrator action:** The orchestrator may apply this one-line fix directly in the commit without invoking a remediation agent, after which the track is eligible for closure.

---

## Detailed Spec & Contract Audit

| Requirement | Source | Evaluation | Status |
|---|---|---|---|
| **Seam discipline** | Review Prompt | Touched only authorized paths: `Dockerfile`, `.dockerignore`, `cmd/thutapi/main.go`, `cmd/thutapi/main_test.go`, `internal/web/templates/shelf.html`, `internal/web/web_test.go`, `dev-diary/PLAN.md`. Verified via `git status --short`. | **PASS** |
| **Go style rules: doc comments** | AGENTS.md §Go style | All new tests (`TestStaticAssets_CacheControlAndFiltering`, `TestDockerfile_OptionAPrewarmAndPermissions`, `TestParseConfig_PrewarmDir`) have descriptive doc comments matching their names. | **PASS** |
| **Go style rules: types & signatures** | AGENTS.md §Go style | No pointer to interface/slice/map. Small structs by value. Errors not mutated or ignored. | **PASS** |
| **Dockerfile Option A prewarm baking** | Option A spec | `COPY --from=builder /src/data/prewarm /prewarm` and `ENV PREWARM_DIR=/prewarm` present in runtime stage. | **PASS** |
| **Nonroot traversal and read permissions** | Dockerfile & Distroless | `RUN chmod -R a+rX /src/data/prewarm` in builder stage ensures nonroot (uid 65532) has read and traversal access. Verified via live container probe. | **PASS** |
| **Dockerignore filtering** | `.dockerignore` | `data/*` ignores transient databases and tokens while `!data/prewarm` permits fixture directory. Build context includes fixture files without runtime DBs. | **PASS** |
| **Static Assets Cache-Control** | Live Bug Report 4 | `staticAssets()` sends `Cache-Control: no-cache, must-revalidate` on all static assets (`.js`, `.css`, etc.). Tested in unit test and live HTTP probe. | **PASS** |
| **Shelf script cache-busting** | Live Bug Report 4 | `shelf.html` specifies `<script type="module" src="/static/app.js?v=2"></script>`. Tested in `internal/web/web_test.go`. | **PASS** |
| **Config parsing for PREWARM_DIR** | `cmd/thutapi/main.go` | `parseConfig()` correctly parses `PREWARM_DIR` from environment. Default behavior preserved when unset. Pinned by `TestParseConfig_PrewarmDir`. | **PASS** |

---

## Live Container Runtime Probe

An end-to-end Docker runtime verification was executed against the built image:
1. `docker build -t thutapi:test-prewarm .`: Completed successfully. Build context sent 14.6 MB (confirming `.dockerignore` admitted `data/prewarm` while ignoring transient data).
2. Container launched as nonroot user (`uid=65532 gid=65532`) with empty `/data` volume:
   - `-prewarm-dir` defaulted to `"/prewarm"`.
   - Cold boot startup successfully imported `Bo and Pip's Moon Mango Dance` (`d625fd608be48227f08c33cf860e5de8`) into SQLite database and media store.
   - `GET /` returned HTTP 200 containing prewarmed book cards and `<script type="module" src="/static/app.js?v=2"></script>`.
   - `GET /static/app.js` returned HTTP 200 with `Cache-Control: no-cache, must-revalidate`.
3. Test container and temporary test artifacts cleaned up completely.

---

## Verification Gates Table

| Gate | Command | Result | Notes |
|---|---|---|---|
| Race detector | `go test -race ./...` | **PASS** | Full workspace test suite passes with race detector active. |
| Fresh execution | `go test -count=1 -race ./...` | **PASS** | Ran fresh across all packages (19 packages, exit 0). |
| Static analysis | `go vet ./...` | **PASS** | Clean, 0 diagnostics. |
| Formatting | `test -z "$(gofmt -l .)"` | **PASS** | Clean, 0 unformatted files. |
| Deployment test suite | `deploy/redeploy_test.sh` | **PASS** | 39 passed, 0 failed across preflight, deploy flow, and rollback gates. |
| Diff whitespace (code) | `git diff --check Dockerfile .dockerignore cmd/thutapi/... internal/web/...` | **PASS** | Clean, exit 0. |
| Diff whitespace (repo) | `git diff --check` | **FAIL** | Exit code 2 at `dev-diary/PLAN.md:3138: new blank line at EOF.` (Finding L1). |

---

## Mutation Testing Probes

Three targeted mutations were introduced, tested against the test suite to confirm test sensitivity, and cleanly restored:

| # | Mutation | Target Test | Command | Result |
|---|---|---|---|---|
| **M1** | Mutate `Cache-Control` header in `cmd/thutapi/main.go:360` to `"public, max-age=3600"` | `TestStaticAssets_CacheControlAndFiltering` | `go test -count=1 ./cmd/thutapi -run TestStaticAssets_CacheControlAndFiltering` | **RED** — fails at `main_test.go:1131`: `/app.js Cache-Control = "public, max-age=3600", want 'no-cache, must-revalidate'` |
| **M2** | Mutate `ENV PREWARM_DIR=/prewarm` in `Dockerfile:92` to `ENV PREWARM_DIR=/broken` | `TestDockerfile_OptionAPrewarmAndPermissions` | `go test -count=1 ./cmd/thutapi -run TestDockerfile_OptionAPrewarmAndPermissions` | **RED** — fails at `main_test.go:1178`: `Dockerfile missing expected directive "ENV PREWARM_DIR=/prewarm"` |
| **M3** | Mutate `src="/static/app.js?v=2"` in `internal/web/templates/shelf.html:2` to unversioned `src="/static/app.js"` | `TestShelf_PackageLevelFunction` | `go test -count=1 ./internal/web -run TestShelf_PackageLevelFunction` | **RED** — fails at `web_test.go:446`: `body lacks cache-busted module script: ...` |

All mutations produced immediate, deterministic test failures and were reverted to a byte-identical state verified via `git status --short`.

---

## Conclusion & Verdict

- **Option A Implementation:** Hermetic, robust, and verified with live container boot and nonroot permissions.
- **Bug 4 Cache-Busting:** Correctly configured on static HTTP asset handler and landing page template.
- **Finding:** 1 Low finding (**L1**) in `dev-diary/PLAN.md:3138` (whitespace error on EOF).
- **Verdict:** **REMEDIATE — 0 C / 0 H / 0 M / 1 L** (Orchestrator fast-path eligible).
