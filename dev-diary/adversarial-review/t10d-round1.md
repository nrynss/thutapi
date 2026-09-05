# T10d round 1 — adversarial review of `internal/mediastore`

| | |
|---|---|
| **Target** | Track T10d — the film's content type (PLAN.md §T10d, contract row C2 from T10c). Working tree on `main`. |
| **Reviewer** | Fresh adversarial Review Agent (`ReviewerT10d`), 2026-09-05. Not the implementer of T10d. |
| **Owns** | `supportedTypes` in `internal/mediastore/mediastore.go` and its test in `internal/mediastore/mediastore_test.go`. |
| **Files changed** | `internal/mediastore/mediastore.go`, `internal/mediastore/mediastore_test.go`, this review record. |
| **Verdict** | **APPROVE — 0 × C, 0 × H, 0 × M, 0 × L** |

---

## Summary of the diff

Track T10d closes contract row C2 identified in T10c (`dev-diary/adversarial-review/t10c-round1.md` §C2), which prevented the production generation pipeline from persisting the completed MP4 film:

1. **`internal/mediastore/mediastore.go`**:
   - Extended package doc comment to mention the MP4 book film alongside image and audio types.
   - Added `"video/mp4": true` to the closed map `supportedTypes`.
   - Updated the doc comment on `supportedTypes` to document the addition of `video/mp4` for the book film from `internal/bookvideo`/`internal/bookgen`.
   - Maintained the strict closed set: did not admit `video/webm` or other unproduced media types.

2. **`internal/mediastore/mediastore_test.go`**:
   - In `TestPersistRejectsUnsupportedContentTypes`, substituted the now-supported `"video/mp4"` with an unsupported video type (`"video/webm"`), preserving the rejection test pin for non-whitelisted video formats.
   - `TestPersistAcceptsSupportedTypes` ranges over `supportedTypes`, automatically executing subtest `TestPersistAcceptsSupportedTypes/video/mp4` to assert successful persistence and metadata storage.
   - Added dedicated pin test `TestPersistAndServeVideoMP4_RangeRequest` testing:
     - Full GET: `200 OK`, `Content-Type: video/mp4`, `Cache-Control` containing `immutable`, quoted `ETag`, and exact byte equality.
     - Sub-slice Range request (`Range: bytes=1024-2047`): `206 Partial Content`, `Content-Type: video/mp4`, `Content-Range: bytes 1024-2047/8192`, and exact slice data match.
     - Suffix Range request (`Range: bytes=-512`): `206 Partial Content`, `Content-Type: video/mp4`, `Content-Range: bytes 7680-8191/8192`, and exact slice data match.

---

## Severity counts

**0 C / 0 H / 0 M / 0 L**

---

## Findings table

*(No findings. Zero defects across all severities.)*

| # | Sev | Where | What | Pin | Mutation |
|---|---|---|---|---|---|
| - | - | - | None | - | - |

---

## Spec & Contract Audit

| Requirement | Evaluation | Status |
|---|---|---|
| **Seam ownership** | Touched ONLY `internal/mediastore/mediastore.go` and `internal/mediastore/mediastore_test.go`. Verified via `git status --porcelain`. | **PASS** |
| **Done when condition** | "a `video/mp4` blob persists and serves back through `GET /media/{id}`, and T10c's film-persist step succeeds against the real store." Verified by `TestPersistAndServeVideoMP4_RangeRequest`, `TestPersistAcceptsSupportedTypes/video/mp4`, and `*mediastore.Store` satisfying `bookgen.filmStore` where `main.go:305` wires `Film: blobs`. | **PASS** |
| **Closed set discipline** | `supportedTypes` remains closed: only `image/png`, `image/jpeg`, `image/webp`, `audio/mpeg`, `audio/wav`, `video/mp4`. Other video formats (e.g. `video/webm`) are rejected. | **PASS** |
| **Range request support** | MP4 serving supports sub-slice and suffix `Range` headers via `http.ServeContent` with `206 Partial Content` and accurate `Content-Range` headers, critical for HTML5 `<video>` seeking/scrubbing and `+faststart`. | **PASS** |
| **Doc comments match behavior** | Package doc and `supportedTypes` doc comment accurately describe the addition of the MP4 film without contradicting implementation or other package docs. | **PASS** |

---

## Verification Gates Table

| Gate | Command | Result |
|---|---|---|
| Build | `go build ./...` | **PASS** (clean, exit 0) |
| Vet | `go vet ./...` | **PASS** (clean, exit 0) |
| Format | `gofmt -l .` | **PASS** (clean, 0 unformatted files) |
| Package tests & coverage | `go test ./internal/mediastore/... -race -cover -count=5` | **PASS** (95.6% statement coverage; floor is 75%) |
| Full workspace tests | `go test ./... -race` | **PASS** (all packages pass with race detector) |

---

## Mutation Testing

Mutations were applied to the working tree, tested against the test suite, and reverted cleanly to verify that tests are load-bearing. Before and after file hashes were verified via `md5sum` (`6e6d829fd400c6b73cb84d6da0bf95b4  internal/mediastore/mediastore.go`, `ef2cabfd9471674c7d912af6771b4168  internal/mediastore/mediastore_test.go`).

| # | Mutation | Target | Command | Result |
|---|---|---|---|---|
| **M1** | Remove `"video/mp4": true` from `supportedTypes` in `internal/mediastore/mediastore.go` | `TestPersistAndServeVideoMP4_RangeRequest` | `go test ./internal/mediastore -run "TestPersistAndServeVideoMP4_RangeRequest"` | **RED** — fails at `mediastore_test.go:248`: `persist video/mp4: mediastore: persist: mediastore: invalid content type: "video/mp4"` |
| **M2** | Revert unsupported video test case back to `{"video", "video/mp4"}` in `TestPersistRejectsUnsupportedContentTypes` | `TestPersistRejectsUnsupportedContentTypes` | `go test ./internal/mediastore -run "TestPersistRejectsUnsupportedContentTypes"` | **RED** — fails at `mediastore_test.go:101`: `--- FAIL: TestPersistRejectsUnsupportedContentTypes/video (0.00s): err = <nil>, want ErrInvalidContentType` |

Both mutations produced clear, expected test failures. Reverting restored the tree to pristine state with zero residue.

---

## Zero-Residue Claim

Track T10d has no prior review round. This round reports zero findings across all severities:
- Critical (C): 0
- High (H): 0
- Medium (M): 0
- Low (L): 0

All requirements of `dev-diary/PLAN.md` §T10d and `AGENTS.md` are satisfied. The contract row C2 recorded in T10c is resolved.

**VERDICT: APPROVE (0/0/0/0) — zero findings, zero residue.**
