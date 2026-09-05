# T10b round 4 — adversarial re-review of the player and C4 recovery

| | |
|---|---|
| **Target** | T10b's server-rendered `/book/{id}` player, C4 snapshot/stream recovery, artifact downloads, and the round-3 unknown-state and ownership remediation. Reviewed against PLAN.md §T10 / C4, the sanctioned C4 consumer seam, and the T9 adapter contract. |
| **Reviewer** | Fresh independent Terra reviewer (`gpt-5.6-terra`), 2026-09-06. |
| **Verdict** | **REMEDIATE — 0 × C, 1 × H, 0 × M, 0 × L** |

## Evidence and prior-round residue

Round-1 H2/H3 remain closed. `BookHandler.load` and `DownloadHandler` keep
previous-run illustrations, PDF, and MP4 inaccessible while the known latest
run is `running`, `failed`, or `unknown`; the server-rendered book controls,
C4 JSON, and named downloads agree. `html/template` escapes the page values;
the cold page has illustration-plus-text composition, native `controls
playsinline` video when finished, conditional PDF and film controls, and
story-slugged attachment filenames while mediastore retains byte-range
serving.

Round-2's initial-connection closure remains effective: C4 waits for the
first `open` before its state read, keeps a state-read failure non-terminal,
and merges a boundary approval with the snapshot. Round-3's `unknown` closure
also remains effective for a remembered but unreadable job: it is an
artifact-free, non-terminal checking state and never posts a duplicate run.
The sanctioned PLAN C4 row names all five external T9/T10c paths and constrains
their responsibilities, so round-3 M1 has no remaining ownership residue.

Read-only verification on this tree:

| Check | Result |
|---|---|
| `go vet ./...`; `CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' ./cmd/thutapi` | PASS |
| `go test ./internal/web ./internal/bookgen ./cmd/thutapi -race -count=3 -cover` | PASS — web 82.6%, bookgen 82.6%, cmd/thutapi 89.6% |
| `go test ./... -race -count=1` | One unrelated failure: `internal/interview.TestRestartedHandlerSeesEndedInterview` timed out waiting for its closing turn. The changed T10b packages passed in that run; `go test ./internal/interview -race -count=5` then passed. |
| `gofmt -l .`; `git diff --check`; `node --check static/app.js static/book/catchup.js static/browser-test.js` | PASS (no diagnostic output) |
| Browser execution | Not run. This environment has no browser surface, so the static harness is inspected and syntax-checked only; it is not evidence of browser execution. |

## Findings

| # | Severity | Where | What | Pin (failing probe or test) | Mutation (what breaks if reverted) |
|---|---|---|---|---|---|
| H1 | H | `static/book/catchup.js:15,38-49`; `static/app.js:254-325,391-399` | C4 synchronizes only once, on the first EventSource `open`, and a newly started generation does not perform a C4 state read at all. The book stream is explicitly from-now-on. After an established SSE connection drops, `EventSource` reconnects and fires another `open`, but the `{ once: true }` listener has been removed and neither `readBookState` nor the adapter re-reads `/book/{id}/state`. Any approvals or terminal event published during the disconnect are therefore irrecoverable: reconnecting after `book_ready` leaves the child indefinitely on the drawing screen. The same event-loss interval exists between a successful fresh `POST /generate` and its eventual SSE subscription because `generate()` only calls `attachBookStream`. This contradicts the comment that automatic EventSource reconnect is enough to preserve live progress, and it leaves the C4 recovery protocol incomplete beyond the initial reload. | Add a browser lifecycle probe with an initial C4 snapshot of `running`, then simulate a dropped connection whose server-side gap contains `book_ready`; dispatch the reconnect `open` and make the next C4 state response `ready`. It must re-read state and navigate to `/book/{id}`. Current code issues no second state read because `open` is one-shot, so it remains drawing. Add the analogous fresh-start probe: delay the first stream `open` after `POST /generate`, make its first C4 state contain an approved page or `ready`, and require that state to be merged/navigated. Current `generate()` has no C4 read. | Restore the present one-shot-open path or remove the fresh-start synchronization. The reconnect probe has no second `/book/{id}/state` request and misses its terminal state; the fresh-start probe loses the page/terminal emitted before SSE subscription. |

## Zero-residue assessment

Round-1's stale-media and named-download findings, round-2's first-open and
state-error findings, and round-3's unknown-state and ownership findings are
closed in their stated paths. H1 is new residue in the same C4 protocol: it
appears after a normal SSE reconnection and on the initial stream of a newly
started generation, neither of which the previous rounds exercised. The
from-now-on stream has no replay mechanism, so an initial snapshot alone
cannot cover those later gaps. There is no zero-residue claim.

**VERDICT: REMEDIATE — 0 × C, 1 × H, 0 × M, 0 × L.**
