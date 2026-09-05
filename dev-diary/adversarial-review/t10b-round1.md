# T10b round 1 — adversarial review of the book player and C4 catch-up

| | |
|---|---|
| **Target** | T10b: server-rendered `/book/{id}`, `static/book/**`, its route lines, and C4's book-state read (PLAN.md §§T10, T10b, “What the player owes”, and C4). |
| **Reviewer** | Fresh independent Terra reviewer (`gpt-5.6-terra`), 2026-09-06. |
| **Verdict** | **REMEDIATE — 0 × C, 3 × H, 0 × M, 0 × L** |

## Scope and evidence

The new page uses `html/template`, so the tested title, byline, page text, and
media URLs are escaped. It renders approved illustrations with their page text,
links the local stylesheet, keeps the PDF action conditional on an artifact,
and uses a native `controls playsinline` video. `GET /book/{id}/state` answers
JSON with `Cache-Control: no-store`; unknown books return the typed JSON 404.
The narrow store and generation interfaces are appropriate, and no new polling
loop, CDN, or unsafe HTML sink was introduced.

Read-only verification on this tree:

| Check | Result |
|---|---|
| `go test ./internal/web ./internal/bookgen ./cmd/thutapi -race -count=1` | PASS |
| `go vet ./internal/web ./internal/bookgen ./cmd/thutapi` | PASS |
| `go test ./internal/web ./internal/bookgen ./cmd/thutapi -cover` | PASS: web 90.3%, bookgen 82.6%, cmd 89.3% |
| `go test ./... -race -count=1` | External failure only: `internal/interview.TestRestartedHandlerSeesEndedInterview` timed out; the same test passed standalone. No T10b file imports or edits that package. |
| `gofmt -l` and `git diff --check` on all T10b Go changes | PASS |

## Findings

| # | Severity | Where | What | Pin (failing probe or test) | Mutation (what breaks if reverted) |
|---|---|---|---|---|---|
| H1 | H | `static/app.js:311-340`; `internal/web/web.go:70-85`; `internal/web/templates/book.html:10` | C4 is published but has no consumer. A cold reload of an ended interview whose generation is already running subscribes only to the interview stream, then unconditionally repeats `POST /interviews/{id}/generate`. The correctly returned 409 `busy` is caught as `failed`; it never reads `/book/{book_id}/state`, never restores approved-page count, and never subscribes to the book SSE before the one catch-up read. The book template supplies only `data-book-state-url` and no script or event-stream URL, so it cannot supply the required subscribe-then-catch-up sequence either. This violates C4's reload-at-minute-four requirement and turns a live run into a false failure screen. The required cross-track correction must be explicitly declared rather than silently extending T10b beyond `static/book/**`. | Add a browser regression that starts from `GET /interviews/{id}` with `{status:"ended",book_id:"b"}`, makes the generate POST return `409 {"error":"busy"}`, and exposes a running C4 state with one approved page. It must subscribe to the book event stream before one `GET /book/b/state`, display one marker, and remain running. Current `static/app.js` calls the POST, never calls `/book/b/state`, and renders “The animals need a little rest.” | After the recovery path is added, restore the current `useEffect`→`generate()` call with the broad `catch` that assigns `failed`; the regression returns to the false failure state and never requests C4. |
| H2 | H | `internal/web/web.go:118-143`; `internal/web/templates/book.html:18-25` | The retry protection applies only to illustration rows. For a latest run in `running` or `failed`, `load` replaces images with `CatchUp.Approved` but retains every prior `application/pdf` and `video/mp4` URL from `BookMedia`. Both `/book/{id}/state` and the SSR page therefore advertise/play an old completed book while their `status` says the latest generation is running or failed. Those artifacts are precisely stale-run media, so they create false completion/progress despite the comment claiming the response is honest. The existing stale-media test covers images only. | Extend `TestBookStateRunningUsesLatestRunNotStaleMedia` with a prior `application/pdf` and `video/mp4` row and assert that a running latest run returns no `pdf_url` or `video_url` and renders neither download nor video. It fails now: `load` assigns both URLs before its running/failed image-only replacement. Repeat for `GenerationFailed`. | After clearing non-current artifacts for running/failed state, restore the current `pdfURL, videoURL := "", ""` collection loop without clearing them in the running/failed branch; the regression exposes `/media/old.pdf` and `/media/old.mp4` again. |
| H3 | H | `internal/mediastore/mediastore.go:270-315`; `internal/web/templates/book.html:19-20` | The finished-film download does not meet T10's required `Content-Disposition: attachment; filename=<slugified-story-name>.mp4` contract. `/media/{id}` sends no `Content-Disposition`, and the HTML's bare `download` attribute supplies no filename; direct media URLs therefore default to inline/opaque-id handling rather than the titled standalone artifact the plan requires. The same opaque direct URL is used by the PDF action. There is no route or test that sets or pins artifact disposition. | Create a finished book titled `Mira & Bramble’s Long Day`, fetch the player’s film-download URL and assert `Content-Disposition: attachment; filename="mira-and-brambles-long-day.mp4"` (and the analogous named PDF download if that surface is retained). Current `mediastore.Store.ServeHTTP` sets only content type, cache control, and ETag, so the assertion fails. | After adding the named download surface/header, remove the `Content-Disposition` assignment; the direct-download regression falls back to the opaque media id and fails. |

## Required remediation scope

H1 needs a recorded, narrow C4 contract row for the T9 consumer (or an
equivalent T10b-owned book-page client that has an SSE route it can actually
derive). It must subscribe first, use the state endpoint once, deduplicate
`page_approved`, and never retry a `busy` generation as failure. H2 must make
the state and SSR artifact set refer to the same latest run. H3 needs a
named-download route or a controlled response wrapper; a bare `download`
attribute does not provide the specified HTTP disposition.

No findings are remediated in this review.
