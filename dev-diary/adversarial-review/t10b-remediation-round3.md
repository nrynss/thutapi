# T10b remediation round 3

This record fixes the one H and one M finding from `t10b-round3.md`. It does
not change the review verdict or the track status.

| # | Where | What | Fix | Pin | Mutation |
|---|---|---|---|---|---|
| H1 | `internal/web/web.go:120-123,254-268`; `static/app.js:295-319`; `internal/web/web_test.go`; `static/browser-test.js` | `GenerationUnknown` represented a remembered run whose job result could not be read, but the state read promoted prior PDF/MP4 rows to `ready`, downloads remained allowed, and the T9 adapter fell through to a new generation POST. That could expose stale artifacts and spend money while the existing run was unresolved. | Treat `unknown` like `running`/`failed` for server artifact selection: retain only the latest run's approved pages, clear PDF and MP4 URLs, and return 404 for artifact downloads. Restrict ready promotion to `not_started` with both durable artifacts. In the adapter, render the recoverable checking state, keep the subscribed book stream open, and wait for its terminal event without posting a new generation. | `TestBookStateUnknownDoesNotExposePriorRunArtifacts` asserts unknown JSON/SSR has only known approved pages, no old artifact URLs or controls, and both named downloads are 404. `testC4UnknownStateIsRecoverable` asserts the open stream remains live, the known page marker is restored, no `/generate` POST occurs, the checking state has no retry door, and a later `book_ready` is handled. `go test ./internal/web -race -count=1`; `node --check static/app.js static/book/catchup.js static/browser-test.js`. | Restore `GenerationUnknown` in the old-artifact readiness promotion or remove it from the download guard and C4 adapter's terminal handling. The Go pin then exposes `old-pdf`/`old-film` or serves them, while the browser pin retries generation or shows the false failure door. |
| M1 | `dev-diary/PLAN.md:1962-1983`; `internal/bookgen/bookgen.go`; `internal/bookgen/pipeline.go`; `internal/bookgen/bookgen_test.go`; `static/app.js`; `static/browser-test.js` | Round 2's seam narrowed the protocol into `static/book/catchup.js`, but the diff still crossed closed T10c and T9 ownership without a row naming the external paths, responsibilities, and boundary. Definition of done item 7 therefore remained unprovable. | Added the exact T10b C4 consumer seam to PLAN: T10c's three bookgen paths expose and pin only the volatile `CatchUp` snapshot and approval-before-publish bridge; T9's two static paths are limited to the screen-5 adapter and browser pin. The row records that T10c and T9 remain owners, cites the existing T10c C4 assignment and closed T9 screen-5 contract as owner-consent evidence, names every external path, and requires a new owner row for any expansion. | The PLAN contract row is the ownership pin: every current non-owned T10b path is listed with one C4 responsibility and a mutation boundary, while `static/book/catchup.js` and `internal/web/**` remain T10b-owned. `git diff --check`, `go test ./internal/web ./internal/bookgen ./cmd/thutapi -race -count=1`, and `node --check static/app.js static/book/catchup.js static/browser-test.js` verify the bounded seam. | Remove the PLAN row or omit any of the five external paths. The ownership preflight again cannot prove that the T10b diff is authorized, and a future edit can silently widen the T9/T10c dependency. |

## Checks

* `go test ./internal/web -race -count=1` — PASS
* `node --check static/app.js static/book/catchup.js static/browser-test.js` — PASS
* `go test ./internal/web ./internal/bookgen ./cmd/thutapi -race -count=3` — PASS
* `go test ./... -race -count=1` — PASS on rerun after one unrelated timing flake in `internal/interview`
* `go vet ./...` — PASS
* `test -z "$(gofmt -l .)"` — PASS
* `git diff --check` — PASS

The browser lifecycle cases are syntax-checked and inspected in this
environment; no browser execution is claimed because no browser surface is
available.
