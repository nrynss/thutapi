# T10b round 5 — adversarial re-review of reconnect C4 synchronization

| | |
|---|---|
| **Target** | T10b's server-rendered `/book/{id}` player, C4 snapshot/stream recovery, named artifact downloads, and round-4 reconnect/fresh-start remediation. Reviewed against PLAN.md §T10/C4, the sanctioned consumer seam, and the T9 screen-5 adapter contract. |
| **Reviewer** | Fresh independent Terra reviewer (`gpt-5.6-terra`), 2026-09-06. |
| **Verdict** | **REMEDIATE — 0 × C, 1 × H, 0 × M, 0 × L** |

## Evidence and prior-round residue

Round-1's stale-artifact and named-download closures remain effective: the
state, SSR page, and named download handler suppress prior-run pages/PDF/MP4
for `running`, `failed`, and `unknown`; a ready artifact download has the
slugged attachment name and delegates range serving to mediastore. The cold
page remains server-rendered through `html/template`, escapes its values,
shows illustration-plus-text pages, includes a native `controls playsinline`
film player when ready, and links both finished artifacts.

Round-2's first-open barrier and non-terminal state-read errors remain
effective. Round-3's unreadable-run handling and ownership seam remain
recorded. Round 4 correctly changes both fresh-start and ordinary reconnect
paths to request a snapshot after an established connection, and the supplied
browser pins cover a reconnect only after the previous snapshot has completed.

Read-only verification on this tree:

| Check | Result |
|---|---|
| `go test ./... -race -count=1` | PASS |
| `go test ./internal/web ./internal/bookgen ./cmd/thutapi -race -count=1` | PASS |
| `go vet ./...`; `gofmt -l .`; `git diff --check` | PASS |
| `node --check static/app.js static/book/catchup.js static/browser-test.js` | PASS |
| Browser execution | Not available: CUA reports no browsers or apps. The static harness is inspected and syntax-checked, not presented as browser-run evidence. |

## Findings

| # | Severity | Where | What | Pin (failing probe or test) | Mutation (what breaks if reverted) |
|---|---|---|---|---|---|
| H1 | H | `static/app.js:276-301`; `static/book/catchup.js:5-37`; `static/browser-test.js:testC4ReconnectRepeatsSnapshot` | A reconnect that arrives while the preceding C4 snapshot is still in flight is silently discarded. `synchronizeBookState` uses one `WeakSet` bit per subscription; the reconnect callback returns while that bit is set. The original snapshot may describe `running`, while the disconnect gap contains `book_ready`; because the from-now-on stream has no replay, the re-established connection needs a *later* snapshot. None is scheduled after the old request settles, so its stale `running` result leaves the child drawing forever. This is the round-4 event-loss defect under ordinary network timing, and the current reconnect pin only exercises the easier sequence where read one already completed before `open` two. | Add a deterministic C4 lifecycle case: hold the initial `/book/book/state` promise after the first `open`; simulate disconnect plus the server publishing `book_ready`; dispatch the reconnect `open`; then resolve the first read as `{status:"running"}`. It must issue a second, post-reconnect state read and navigate when that read returns `{status:"ready"}`. Current code records only one state request: the reconnect observes the in-flight bit and no deferred synchronization exists. | Restore the current single in-flight guard without a queued/revisioned reconnect read. The delayed-read reconnect pin makes one snapshot request, applies its stale running state, and misses the terminal state published in the gap. |

## Zero-residue assessment

Rounds 1–3 have no observed residue in their stated paths. Round 4's completed-read reconnect and fresh-start pins also pass their stated cases, but H1 remains in the same reconnect protocol when a connection re-establishes before its prior state read completes. Since that timing can drop `book_ready` or approvals from the non-replaying stream, the track has no zero-residue claim.

**VERDICT: REMEDIATE — 0 × C, 1 × H, 0 × M, 0 × L.**
