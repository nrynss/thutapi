# T9 round 6 — final adversarial re-review of the frontend shell and interview UI

| | |
|---|---|
| **Target** | T9 — Frontend shell and interview UI (`dev-diary/PLAN.md` §T9), after `t9-remediation-round5.md`. |
| **Reviewer** | Fresh independent review agent (`gpt-5.6-terra`), 2026-09-06. Not an implementation or remediation agent for this track. |
| **Owns audited** | `static/**`, `internal/web/**`, T9's human-page/static route lines, the authorized narrow T4 catch-up replay correction, and the narrow T9a iframe bridge/theme hand-off. |
| **Verdict** | **APPROVE — 0 × C, 0 × H, 0 × M, 0 × L** |

## Scope and verification

The round-5 repair is sound. `silentUnlockURL` decodes to a 46-byte RIFF/WAVE
PCM asset: RIFF size 38 equals byte length minus eight; `fmt ` is PCM, mono,
8 kHz, 16-bit, with block alignment two; the `data` chunk is two bytes and
contains one complete zero-valued 16-bit sample. This is a valid aligned silent
PCM frame, unlike the one-byte round-5 finding. The shelf gesture plays this
source while muted, waits for that play promise before it marks the sole shared
player unlocked, then reuses that same player for a question clip. A failed
gesture leaves the speaker tap fallback. The deterministic fake decodes the
data URL and rejects absent sources, malformed RIFF/fmt/data identifiers,
inconsistent RIFF/data sizes, non-PCM16-mono fields, incomplete sample frames,
and non-silent payloads before it resolves `play()`. Thus the prior malformed
source cannot regain a passing test merely by being nonempty.

Re-opened lifecycle and ordering checks found no residue: the POST-start
hand-off creates and buffers the interview EventSource before route adoption;
the server's typed `opening`/`current` replay covers the earlier pre-201
publication window; equal live/catch-up turns preserve the live chips; late
audio is accepted only for the active turn; and terminal catch-up closes the
interview stream before the independent generation stream opens. The book
stream counts distinct valid page numbers, resets on retry, treats
`narration_unavailable` warmly, and closes on `book_ready` or `failed`. The
iframe consumes the T9a widget and supplies only the bounded done number.

The direct no-sample path is still the permitted T9/T13 boundary while T13 has
no complete browser mount; no inert record/upload surface is advertised. The
server-rendered shelf/interview shells, local vendored Preact/htm/hooks,
same-document audio lifetime, singular human versus plural API routes,
template/Preact escaping, and absence of external CDN or untrusted HTML sinks
remain intact.

Read-only verification passed:

| Gate | Command | Result |
|---|---|---|
| Full suite and race detector | `go test ./... -race` | PASS |
| Vet, formatting, whitespace | `go vet ./...`; `test -z "$(gofmt -l .)"`; `git diff --check` | PASS |
| Static JavaScript syntax | `node --check static/app.js`; `node --check static/browser-test.js` | PASS |
| T9a contract | `go test ./static/race -race`; `cmp -s static/race/race.html static/race/index.html` | PASS |
| WAV structural pin | Decode `silentUnlockURL`; assert RIFF/data sizes, PCM16 mono fields, block alignment, and zero sample | PASS (`bytes=46`, `riffSize=38`, `dataSize=2`, `blockAlign=2`) |

The browser contract contains the behavioral gesture/audio mutation pin and
the event-ordering, cleanup, no-T13, race, and iframe probes. No physical
phone or deployable HTTPS endpoint is available to this reviewer. Per the
user's explicit decision, the existing manual checklist and pending operator
record are accurately retained as deferred operator evidence and are not a
code-closure finding.

## Findings

None.

## Zero-residue claim

There is zero residue from every prior round: round 1's opening-error recovery,
same-document audio lifetime, palette/race integration, catch-up handling, and
browser coverage; round 2's distinct approval counting, not-found recovery,
equal-turn ordering, T13 boundary, and iframe bridge; round 3's hand-off and
terminal-SSE cleanup; round 4's pre-201 authoritative replay and gesture
unlock; and round 5's PCM frame validity are all present and pinned. The
physical-phone item is deliberately and honestly deferred operator evidence by
the user decision, rather than an unresolved implementation defect.

**VERDICT: APPROVE (0/0/0/0).**
