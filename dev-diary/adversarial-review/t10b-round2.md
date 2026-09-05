# T10b round 2 — adversarial re-review of the book player and C4 remediation

| | |
|---|---|
| **Target** | T10b: the server-rendered `/book/{id}` page, `static/book/**`, its route lines, C4's book-state read, and the round-1 remediation consumer in the T9 shell. Reviewed against PLAN.md §T10's cold-link/player/C4 requirements and the T10c/T10f/T10g contracts. |
| **Reviewer** | Fresh independent Terra reviewer (`gpt-5.6-terra`), 2026-09-06. |
| **Verdict** | **REMEDIATE — 0 × C, 2 × H, 1 × M, 0 × L** |

## Evidence and retained round-1 closures

Round-1 H2 is closed: `BookHandler.load` discards old illustrations, PDF and
MP4 for a running or failed current run, and `DownloadHandler` refuses those
states. `TestBookStateRunningUsesLatestRunNotStaleMedia` and
`TestBookStateReportsFailedLatestRunWithoutInventingArtifacts` cover both JSON
and SSR controls. Round-1 H3 is closed: named `/book/{id}/download/{kind}`
routes set a title-derived ASCII attachment filename and delegate byte/range
serving to mediastore; the tested apostrophe, ampersand, Unicode punctuation,
and fixed extension do not form a header injection path. The HTML remains
server-rendered with `html/template`, escaped story values, visible page text
under each illustration, native `controls playsinline` video, conditional PDF
and MP4 controls, a local book stylesheet, and usable semantic labels.

Read-only verification on this tree:

| Check | Result |
|---|---|
| `go vet ./...`; `go build ./...` | PASS |
| `go test ./internal/web ./internal/bookgen ./cmd/thutapi -race -count=3 -cover` | PASS — web 82.0%, bookgen 82.6%, cmd/thutapi 89.6% |
| `go test ./... -race -count=1` | FAIL external: only `internal/interview.TestRestartedHandlerSeesEndedInterview` timed out. It passed standalone with `-race -count=5`; all T10b packages passed in the workspace run. |
| `gofmt -l .`; `git diff --check`; `node --check static/app.js static/browser-test.js` | PASS (no output) |

The browser contract file contains the expected round-1 recovery case, but it
does not model an EventSource connection opening or a failed C4 read, leaving
the two live lifecycle defects below unpinned.

## Findings

| # | Severity | Where | What | Pin (failing probe or test) | Mutation (what breaks if reverted) |
|---|---|---|---|---|---|
| H1 | H | `static/app.js:250-322` (`attachBookStream`, `recoverGeneration`); `static/browser-test.js:185-205` | The claimed **subscribe-then-read** is only constructor order. `new EventSource(...)` starts an asynchronous connection; it does not prove the server has registered the book-topic subscription. `recoverGeneration` immediately starts `GET /book/{id}/state`. If state snapshots three pages, page four or the terminal event is published before the SSE request reaches `ServeTopic`, that event is neither in the snapshot nor in the from-now-on stream. A reload can therefore undercount real progress indefinitely or spin forever after a missed `book_ready`. C4 explicitly exists to prevent this gap. | Extend the browser fake with a controllable `open` event. Start an ended reload with C4 state held at three pages, assert **no** state fetch occurs before the book EventSource opens, then open it and return the snapshot. The current code calls `/book/book/state` before any `open`; the assertion fails. The test should also inject a page/terminal event in the former constructor-to-open interval and prove the post-open snapshot/stream cannot lose it. | Replace the required open-acknowledged subscribe with the present constructor-followed-immediately-by-fetch sequence. The regression sees the state request before `open` and recreates the missing-event window. |
| H2 | H | `static/app.js:309-322` | Any failed C4 state request is converted into a terminal `failed` UI state and explicitly closes the already-open book stream. A transient 500, dropped fetch, or temporary store fault says nothing about the generation job; the job can be running and the stream can still receive `page_approved` or `book_ready`. Closing it manufactures the failure screen and suppresses the real terminal event, inviting a misleading retry screen for a live, billable run. Only the server's `failed {}` event may make the job terminal. | Add a browser case where the ended reload's `/book/book/state` returns 500 after the book SSE opens. It must keep the source alive and avoid the failure doors; dispatching a later `book_ready` must still navigate to `/book/book`. Current `catch` closes the source and renders “The animals need a little rest.” | Restore `source.close(); setBookState("failed")` in the C4-read error path. The regression closes a live source and displays the false terminal state again. |
| M1 | M | `static/app.js`; `static/browser-test.js`; `dev-diary/PLAN.md:112-120,1516-1518`; `t10b-remediation-round1.md` | The remediation implements the C4 consumer in `static/app.js` and its browser test. Those are T9's `static/**` ownership, while T10b owns only `static/book/**` plus `internal/web/**` and route lines. The remediation record describes a “narrow cross-track contract row,” but it neither amends T10b's `Owns` line nor records a transfer/reopened T9 seam. The protocol permits no general cross-track exception: Definition of done item 7 therefore remains false. | Add an ownership preflight to the remediation: compare every changed path to T10b's PLAN `Owns`, and require a PLAN-approved transfer/sanctioned C4 seam before it accepts `static/app.js` or `static/browser-test.js`. It fails on the current two paths. | Remove the ownership assertion or leave the current prose-only declaration. The track can again change T9-owned files without the required contract/owner record. |

## Required remediation

H1 requires an actual subscription-ready barrier before the one C4 snapshot,
with page-number de-duplication retained. H2 requires treating a failed state
read as unknown/recoverable transport state while the subscribed SSE remains
authoritative; it must not synthesize a terminal result. M1 requires the
orchestrator to formalize the C4 consumer seam before a remediation agent
changes T9-owned files, or to move that consumer into a T10b-owned surface.

## Zero-residue assessment

Round-1 H2 and H3 have zero residue as described above. Round-1 H1 is only
partially closed: the normal running reload now avoids a repeated generation
POST, restores deduplicated snapshot pages, and leaves a running source open,
but H1 remains at the connection-establishment boundary (new H1) and when the
C4 fetch itself fails (new H2). The ownership defect (M1) independently blocks
the track's Definition of done. There can be no APPROVE or zero-residue claim.

**VERDICT: REMEDIATE — 0 × C, 2 × H, 1 × M, 0 × L.**
