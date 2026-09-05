# T9 round 5 — adversarial re-review of the frontend shell and interview UI

| | |
|---|---|
| **Target** | T9 — Frontend shell and interview UI (`dev-diary/PLAN.md` §T9), after `t9-remediation-round4.md`. |
| **Reviewer** | Fresh independent review agent (`gpt-5.6-terra`), 2026-09-06. Not an implementation or remediation agent for this track. |
| **Owns audited** | `static/**`, `internal/web/**`, T9's human-page/static route lines, the authorized narrow T4 catch-up replay correction, and the narrow T9a iframe bridge/theme hand-off. |
| **Verdict** | **REMEDIATE — 0 × C, 1 × H, 0 × M, 0 × L** |

## Scope and verification

Round 4's pre-201 catch-up correction is present: `GET /interviews/{id}` has
an additive, typed `current` replay containing the latest question turn, text,
chips, and optional audio URL; the existing 201 response may additionally carry
the same shape as `opening`. The UI subscribes first, accepts the POST replay
or authoritative catch-up before legacy session storage, preserves a live
same-turn question, and does not poll. The server updates current audio only
when it belongs to the current question, so a late clip from an earlier turn
cannot overwrite a later question. The response remains JSON-compatible for
existing clients: all new fields are optional and no existing field changes
type.

The one-player path still has one `new Audio()` allocation, uses its promise as
the gate before a non-gesture question clip plays, and renders the cold-link
speaker affordance if that gate fails. The no-T13 path remains the allowed
direct no-sample generation path. The two SSE lifecycles close on their
terminal events, distinct valid page ordinals count as approvals, retry resets
that count, the T9a iframe receives only its bounded done number, and
`narration_unavailable` remains a warm successful state. No invented polling,
untrusted HTML sink, CDN dependency, page/API route collision, or broader T4
change was found.

Read-only checks passed:

| Gate | Command | Result |
|---|---|---|
| Full suite and race detector | `go test ./... -race` | PASS |
| Vet, formatting, and whitespace | `go vet ./...`; `test -z "$(gofmt -l .)"`; `git diff --check` | PASS |
| Static JS syntax | `node --check static/app.js static/browser-test.js` | PASS |
| T9a contract | `go test ./static/race -race`; `cmp -s static/race/race.html static/race/index.html` | PASS |
| WAV decoder recognition | pipe the embedded data URL through `ffprobe` | recognized as WAV/PCM, but this parser is permissive and does not make its incomplete PCM frame valid |

The physical-phone check remains deliberately deferred deployment-time
operator evidence per the user decision. The existing record accurately makes
no device claim and names the HTTPS probe and fields still required; it is not
reported here as code residue.

## Findings

| # | Severity | Where | What | Pin (failing probe or test) | Mutation (minimal reverting edit) |
|---|---|---|---|---|---|
| H1 | H | `static/app.js:17,43-62`; `static/browser-test.js:15-19,102-118` | The purported silent PCM WAV is malformed for its declared format. Its RIFF header declares 16-bit mono PCM (`blockAlign: 2`) but its `data` chunk has exactly one byte. That is an incomplete PCM sample frame (and an unpadded odd-length RIFF data chunk), so it is not a valid decodable silent PCM asset. `ffprobe` recognizes the container but reports no duration; it does not validate this frame invariant. A browser can reject or immediately end it, so the gesture may not establish the autoplay entitlement that question audio relies on. The fake only rejects an empty `src`, therefore it certifies a malformed nonempty URL rather than successful playable audio. | `node - <<'NODE'
const b=Buffer.from("UklGRiUAAABXQVZFZm10IBAAAAABAAEAQB8AAIA+AAACABAAZGF0YQEAAAAA","base64");
const align=b.readUInt16LE(32), size=b.readUInt32LE(40);
if (size % align !== 0 || b.length !== 44 + size) process.exit(1);
NODE` exits 1 (`size=1`, `align=2`, `bytes=45`). Replace the source with a complete, padded PCM WAV containing at least one 16-bit zero sample and make `FakeAudio` validate the decoded frame alignment before resolving `play()`; the probe and gesture contract then pass. | Replace the repaired aligned silent WAV with the present one-byte payload, or relax the fake back to accepting every nonempty source; the frame-validity/gesture pin fails to detect the broken unlock asset. |

## Zero-residue claim

Round-1 H1–H3 and M1–M4, round-2 H1–H2 and M1–M4, round-3 M1–M3, and
round-4 M1 are addressed in the current tree. Round-4 H1 is **not** fully
resolved: the replacement gesture source is structurally malformed PCM, as
described in H1 above. The deferred physical-phone operator evidence remains
accurately recorded and is intentionally outside this code-closure verdict.

**VERDICT: REMEDIATE (0/1/0/0).**
