# T12 round 2 — adversarial re-review (fresh reviewer)

| | |
|---|---|
| **Target** | Track T12 — music bed (PLAN.md §T12). Round-2 scope = T12's delta: `internal/audio/music.go` + `music_test.go` (remediation round-1 state), the `internal/audio/{audio,audio_test,narrate_test,wire_test}.go` diffs, `internal/gmi/media/client.go`+`client_test.go` SynthesizeMusic region, `internal/bookvideo/{types,video,bookvideo_test}.go`, `internal/bookgen/{bookgen,video,pipeline,harness_test,pipeline_test,live_test}.go`, plus `t12-round1.md` → `t12-remediation-round1.md`. |
| **Evidence** | AGENTS.md; the round-1 review and remediation files in full (H1/L1 chain, validation table, measurement-method decision); current `music.go`/`music_test.go` in full; `audio.go` diff, `wire_test.go` seam pin, media-client music region + raw-wire pins, `bookvideo` total arithmetic (video.go:574-586, types.go cards 3.5/3.0 s, captionHold), `bookgen` renderFilm/mixFilm seams; real-file probes against `data/live/cache/audio/page-01..08.mp3` + `data/live/t12-music-bed*.mp3`; full gate battery. No live GMI calls. |
| **Method** | Fresh reviewer with **no prior T12 round** — every closure re-established from raw bytes, not from the remediation's prose. Reviewer probes ran as throwaway tests in a frozen snapshot `/tmp/t12r2` (byte-identical copy of the shared tree incl. `data/live` evidence; md5-verified). Mutants were re-applied to the **shared tree** with md5-verified byte-identical restores, plus the snapshot's real-file probe as an extra pin. READ-ONLY except this file and the transient mutant re-runs. |
| **Reviewer / Date** | `T12Round2Reviewer`, 2026-09-06. Shared-tree note: sibling lanes hold the dirty `cmd/**`, `static/**`, `.claude/`, `internal/audio/clone.go`/`clone_test.go`, T13 review files; the tree evolved during this review (T13 added `t13-round4.md` mid-flight). Nothing external is counted against T12. |

## Verdict

**APPROVE — 0 C / 0 H / 0 M / 0 L**

## Severity counts

**0 C / 0 H / 0 M / 0 L**

## Findings table

| # | Sev | Where | What | Pin | Mutation |
|---|---|---|---|---|---|
| — | — | — | None. Round-1's H1 and L1 are both closed (verified below, independently). Round-2 spot mutations of the mix/measure path (MB1–MB4) all go red where they should. | — | — |

## H1 closure — independently verified (the Info/Xing branch yields the PLAYABLE length)

**Semantic read from the code** (`music.go:642-736`): when the first frame's payload starts `Xing`/`Info`, flags at +4; declared frame count at +8 when `flags&1`; the LAME/Lavf/Lavc encoder tag is located after the flag-gated fields (frames/bytes/TOC/quality); the 24-bit word at **tag+21** carries `delay = v>>12`, `padding = v&0xFFF`, read **only** when the tag's first four bytes are exactly `LAME`/`Lavf`/`Lavc`; length = `(frames×spf − delay − padding) / sample_rate`; pads exceeding the declared frames → `ErrClipDuration`; a bare Info header with no encoder tag keeps the untrimmed declared product; a header-less stream keeps the CBR byte-count fallback. That is ffmpeg's mp3-demuxer playable-length semantic (`(frames × spf − delay − padding)` — mp3dec.c `mp3_parse_vbr_tags`/`mp3_parse_info_tag` layout, tag field at +21, 12+12 bit split), and the empirical check below shows it reproduces exactly what ffmpeg delivers on every real file.

**Independent raw-byte probe** (my own parser walk, not `measureDuration`): all 10 real files carry `Info` headers (flags 0x0f), encoder tags `Lavc` (clips) / `Lavf` (beds), gapless words at tag+21:

| File | tag | word | delay | padding |
|---|---|---|---|---|
| page-01.mp3 | Lavc | 0x240630 | 576 | 1584 |
| page-04.mp3 | Lavc | 0x2402f4 | 576 | 756 |
| t12-music-bed2-gibberish.mp3 | Lavf | 0x240240 | 576 | 576 |

— byte-identical to the remediation's claims.

**Independent re-measurement: current Go `measureDuration` vs ffprobe** (criterion: each < ~5 ms). All 10 files re-measured with the actual code; ffprobe and a full ffmpeg PCM decode as the ground truth:

| File | Go measureDuration | ffprobe | decode | delta |
|---|---|---|---|---|
| page-01.mp3 | 4.000000 s | 4.000000 s | 4.000000 s | 0.000 ms |
| page-02.mp3 | 5.000000 s | 5.000000 s | 5.000000 s | 0.000 ms |
| page-03.mp3 | 6.000000 s | 6.000000 s | 6.000000 s | 0.000 ms |
| page-04.mp3 | 3.000000 s | 3.000000 s | 3.000000 s | 0.000 ms |
| page-05.mp3 | 4.000000 s | 4.000000 s | 4.000000 s | 0.000 ms |
| page-06.mp3 | 5.000000 s | 5.000000 s | 5.000000 s | 0.000 ms |
| page-07.mp3 | 6.000000 s | 6.000000 s | 6.000000 s | 0.000 ms |
| page-08.mp3 | 3.000000 s | 3.000000 s | 3.000000 s | 0.000 ms |
| t12-music-bed.mp3 | 56.540589569 s | 56.540590 s | 56.540590 s | <1 µs |
| t12-music-bed2-gibberish.mp3 | 110.132244897 s | 110.132245 s | 110.132245 s | <1 µs |

Every clip is exact (its trimmed sample count divides 44100); the beds agree at the µs scale. The requirement (≥3 clips + one bed, each <5 ms) is met with three orders of magnitude to spare. The round-1 overstatements (+30.20…+48.98 ms per clip) are gone.

**H1-revert mutant re-applied** (drop `− startPad − endPad`, music.go:672): RED on the committed pins (`TestMeasureDuration_InfoGaplessTagTrimsToPlayableLength`, `TestMeasureDuration_GaplessPadsExceedingFramesRejected`) and RED on my real-file probe 10/10 (+26.1…+49.0 ms over ffprobe — exactly the round-1 numbers). Restored byte-identical (md5 `ab1f74eb…`; mutant md5 `f8f1ada8…` — matches the remediation's own record of the same mutant).

## L1 closure — the Info/Xing branch is now pinned

`music_test.go` `infoFixture` mirrors page-01's parseable region: a real `Info` header (flags 0x0f, fields in place) plus a `Lavc63.1.101` tag whose word `0x24 0x06 0x30` at tag+21 packs delay 576 / padding 1584, with the declared count (155) deliberately exceeding the playable decode (4.000000 s) exactly as the real GMI files do. Three pins exercise the branch: the gapless trim to 4.000000 s (with a fixture self-guard that the untrimmed product differs), the bare-Info no-tag untrimmed answer, and the pads-exceed-frames `ErrClipDuration` branch. **MA6 (frame count read at a shifted offset `payload+9:payload+13`) re-applied: RED on all three pins** (page-01 fixture → 17m16.49 s garbage) and on my real-file probe (10/10). Restored byte-identical (mutant md5 `54f2f700…`, matching the remediation's record).

## End-to-end (requirement 3) — independently replicated

Replica render of the 8 real cached clips through `bookvideo.Render` (default cards), each page's `Duration` = the current `measureDuration` output:

- Render total = **42.500000 s** = 3.5 s title + Σ 36.0 s of playable page holds (4+5+6+3+4+5+6+3, each measured exactly) + 3.0 s end card — the arithmetic checks.
- Real film (ffprobe) = **42.523220 s** → film − total = **+23.220 ms**, one 1024-sample AAC frame of mux padding, the same sign and magnitude round-1 measured on the silent-tier control. Requirement ("within ~0.1 s of the real film") met with 4× margin.
- Real-bed mix anchored to the renderer's 42.5 s total (110.2 s keeper bed, trim case): mixed duration preserved (42.523 s); bed level through the body peak 5802; tail peak in [42.380, 42.5] = **25** vs the near-silence threshold 967 — the fade reaches silence at film end, never after it.

## Round-1 PASSes — reconfirmed (nothing regressed)

- **C1** raw-wire pins green: `TestSynthesizeMusic`, `TestSynthesizeMusic_RawWirePinned` (byte-exact body, empty prompt key omitted), `TestSynthesizeMusic_EmptyLyricsRejected`; seam pin `var _ Music = (*media.Client)(nil)` (wire_test.go) builds.
- **Mix core** pins green, incl. real ffmpeg: `TestMixBed_CommandPinned_FadeEndsAtFilmTotal` (byte filter + `st+d == total`), `TestMixBed_CustomGainAndFade`, and `TestMixBed_RealFFmpeg_WindDown` **ran, not skipped** (looped-short-bed and trimmed-long-bed windowed peaks).
- **Bed default payload pin** (`TestGenerateMusicBed_DefaultsReachTheSeam` — gibberish-vocalise lyrics + wordless prompt to minimax-music-3.0), overrides, `ErrNoBed` decode branches, download errors: green.
- **Degrade semantics + nil-guard** green: `TestPipeline_MusicOnMixesTheBedUnderTheFilm` (persists the MIXED row with the renderer's total), `TestPipeline_MusicTransientFailureDegradesToThePlainFilm` (gmi.ErrTransient → plain film, `book_ready`), `TestPipeline_MusicHardFailureFailsRun`, `TestPipeline_MixFailureFailsRun` (no film row), `TestPipeline_EndToEnd` (music-off plain film; `renderFilm`'s `if h.cfg.Music != nil` guard), narration-outage degrade.
- **C2/C3** duration-carry pins green: `TestNarrateBook_DefaultsReachTheCalls` (Clip.Duration = fixture 2.011428571 s), `TestNarrateBook_UnmeasurableClipFailsLoudly` (ErrClipDuration, nothing persisted), Render narration-without-Duration / Duration-without-narration refusals.

## Mutation testing (round 2 — re-applied by me; restores md5-verified byte-identical `ab1f74ebaa7c2ae34969b0eacbe34abf`)

| # | Mutation | Pin run | Result |
|---|---|---|---|
| **H1 revert** | `music.go`: drop `− startPad − endPad` (return the declared product) | `TestMeasureDuration_*` (shared tree) + real-file probe (snapshot) | **RED** — both gapless fixture pins fail; real files 10/10 at +26.1…+49.0 ms over ffprobe. (mutant md5 `f8f1ada8fcb37ce61fdbb5052d3d71de`; restored `ab1f74eb…` OK) |
| **MA6 (L1)** | `music.go`: Info frame count read at a shifted offset (`payload+9:payload+13`) | `TestMeasureDuration_*` + real-file probe | **RED** — all three Info fixture pins fail (17m16.49 s on the 155-frame fixture); real files 10/10. (mutant md5 `54f2f700f115ea3569d29c3e8b61fb39`; restored OK) |
| **MB1 (spot)** | `music.go`: drop `:normalize=0` from amix | `TestMixBed_CommandPinned_FadeEndsAtFilmTotal`, `TestMixBed_CustomGainAndFade` | **RED** — byte-for-byte filter/command pins (the narrated track would be halved the moment a bed appears). (mutant md5 `8973ccb6…`; restored OK) |
| **MB2 (spot)** | `music.go`: `duration=first` → `duration=longest` | same two command pins | **RED** — byte pins (would also hang the short-bed loop case on the endless `-stream_loop -1` input). (mutant md5 `add43c43…`; restored OK) |
| **MB3 (spot)** | `music.go`: gapless 24-bit word read at tag+20 instead of tag+21 | `TestMeasureDuration_*` | **RED** — `InfoGaplessTagTrimsToPlayableLength` yields 4.025578231 s (wrong trim) and `GaplessPadsExceedingFramesRejected` no longer errors. (mutant md5 `add561aa…`; restored OK) |
| **MB4 (spot)** | `music.go`: encoder-tag whitelist narrowed to `Lavc` only (Lavf/LAME lose the trim) | committed pins + real-file probe | Committed pins **GREEN** (fixture is Lavc); my real-file probe **RED** — both Lavf beds over-measure by +40.6/+26.1 ms, exactly the H1 bug shape on files the committed suite does not cover. Demonstrates the real-file measurements carry the Lavf/LAME breadth the fixtures cannot. (mutant md5 `c65738c4…`; restored OK) |

## Verification gates

| Gate | Command | Result |
|---|---|---|
| Build | `go build ./...` (shared tree, full module) | **PASS** (exit 0) |
| Vet (T12 packages) | `go vet ./internal/audio ./internal/gmi/media ./internal/bookvideo ./internal/bookgen` | **PASS** (exit 0) |
| Tests + coverage, audio | `go test ./internal/audio -race -count=1 -cover` | **PASS** — 83.1 % (floor 75 %); real-ffmpeg wind-down ran, not skipped |
| Tests + coverage, gmi/media | `go test ./internal/gmi/media -race -count=1 -cover` | **PASS** — 91.9 % (floor 85 %) |
| Tests + coverage, bookvideo | `go test ./internal/bookvideo -race -count=1 -cover` | **PASS** — 88.4 % (floor 75 %) |
| Tests + coverage, bookgen | `go test ./internal/bookgen -race -count=1 -cover` | **PASS** — 82.1 % (floor 75 %) |
| Format | `gofmt -l` on all 17 T12 files | clean (no output) |
| Round-1 pin spot-checks | named real-ffmpeg / C1 / pipeline degrade / hard-fail / mix-fail runs | **PASS** (each listed by name above) |

External attributions: cmd/interview/static suites are other lanes' mid-edit territory (cmd data-race flake and interview timeout recorded by round-1 as external); not part of T12's scope and not re-run here as gates. The shared tree's own `go build ./...` compiled cmd against the sibling lane's dirty files without complaint.

## cmd cutover dependency — recorded as a stated boundary, not a finding

T12's committed tree does not include cmd's wiring (left for the orchestrator/sibling lane by design, round-1 §Files). The sibling lane's dirty `cmd/thutapi/main.go` + `main_test.go` carry the T12-compat hunks — verified present at review time: `failRenderer.Render` returns `(time.Duration, error)` (main_test.go:136-137) and `run()`'s bookgen Config wires `Music: mediaCli` (main.go:407). cmd's test build therefore depends on the sibling lane's commit of those files; until that commit lands, a T12-only tree cannot build cmd's tests. Stated boundary, not a finding (the migration itself is outside T12's seam and already landed in the sibling lane's work).

## Zero-residue claim (round 1 → round 2, severity by severity)

- **H1 (H, round-1)** — CLOSED. The Info/Xing branch now returns the playable length via the ffmpeg mp3-demuxer semantic (declared frames × spf − gapless delay − padding, tag-gated on LAME/Lavf/Lavc). Independently re-measured: all 8 real clips exact and both beds <1 µs off ffprobe; the H1-revert mutant goes red on the committed pins and on 10/10 real files; the end-to-end replica lands at +23.220 ms (one AAC mux frame), 4× inside the ~0.1 s requirement, with the fade at silence by film end.
- **L1 (L, round-1)** — CLOSED. The Info/Xing branch is pinned by `infoFixture` (real Info header + Lavc tag, declared count exceeding the playable decode); the MA6 shifted-offset mutant is red on the pins and on the real files.
- **Round-1 C1–C4 PASSes** — reconfirmed green under `-race -count=1`, including the real-ffmpeg windowed-peak wind-down and the full degrade/hard-fail matrix.
- **Round-2** — no new findings at any severity. All probes, scratch tests and tmp artifacts removed; the shared tree is byte-identical to the review start except this file (T12 file md5s verified before and after the mutant runs: music.go `ab1f74eb…`, music_test.go `e7e11a60…`, audio.go `8095ed1d…`).

**Verdict: APPROVE — 0 C / 0 H / 0 M / 0 L** (zero residue against all prior T12 rounds).
