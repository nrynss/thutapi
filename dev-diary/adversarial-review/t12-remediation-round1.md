# T12 remediation round 1

**Track:** T12 — music bed (PLAN.md §T12).
**Agent:** T12Remediator. **Base:** shared tree @ `69f0531` + T12 working tree (round-1 state).
**Date:** 2026-09-06.
**Scope touched:** `internal/audio/music.go` (measureDuration), `internal/audio/music_test.go` (Info-header fixture + pins), this file. Nothing else in the shared tree was modified (scratch probes ran in a HEAD worktree and were removed).

## Findings table

| # | Finding | Disposition | Status |
|---|---|---|---|
| **H1** | `measureDuration`'s Info/Xing branch multiplies the DECLARED frame count by samples-per-frame, overstating real GMI clips by ~1.2–1.9 MPEG frames (~30–49 ms/clip): page-01 declares 155 frames → Go measured 4.04898 s, but ffmpeg plays exactly 4.000000 s (decoded PCM byte count, ffprobe, replica render all agree). An 8-page narrated book rendered 42.523 s while Render returned 42.810 s (+0.287 s), so MixBed's end fade (st=total−2s) reached zero ~0.29 s AFTER the film ended — amix=duration=first cut the bed at a 6–14 % residual instead of silence at film end. | **FIXED** — `mp3Duration` now returns the PLAYABLE length: Info/Xing declared frames minus the encoder tag's gapless delay+padding samples (the exact ffmpeg mp3-demuxer semantic), with a CBR fallback and a no-tag Info path unchanged. Real-file validation: all 8 clips + both beds land within 1 µs of ffprobe (table below). | CLOSED |
| **L1** | The Xing/Info branch is unpinned — committed fixtures are CBR-only (`makeMP3`: ID3v2 + plain frames, no Info header), so the branch real GMI clips take was untested; mutant MA6 (frame count read at a shifted offset) passed the whole suite GREEN. | **FIXED** — `music_test.go` adds `infoFixture`, whose first frame carries a REAL "Info" header (flags 0x0f, fields byte-identical to page-01's parseable region) plus a "Lavc" tag with the real gapless word 0x240630 (delay 576 / padding 1584), so the declared count (155) exceeds the playable decode (4.000000 s) exactly as GMI's Lavc files do. Three pins exercise the branch; MA6 is now RED on them (below). | CLOSED |

## Measurement-method decision

The fix's two suggested alternatives were probed against the real files and **both fail** the ≤5 ms criterion:

| Method | page-01 result | vs ffprobe 4.000000 s |
|---|---|---|
| (a) walk the actual frames (parse each header, advance by real frame length incl. padding) | **156** physical frames → 4.075102 s | **+75.1 ms** |
| (b) whole-file byte arithmetic against the first frame's bitrate | 65200 audio bytes / 417.96 B = 156.0 frames → 4.075102 s | **+75.1 ms** |
| Info declared count alone (pre-fix) | 155 frames → 4.048980 s | +49.0 ms |
| **Info declared count − gapless pads (chosen)** | (155×1152 − 576 − 1584)/44100 = **4.000000 s** | **0.0 ms** |

**The brief's CHECK is settled: GMI's clips ARE truly CBR (stream bit_rate 128000; frames alternate 417/418 bytes with the padding bit), with a gratuitous "Info" header — not VBR.** Byte-count CBR arithmetic still fails because the encoder wrote one physical pad frame beyond the playable count (156 physical vs 155 declared, and the mp3 demuxer delivers exactly the declared count — `nb_read_packets=155` observed on page-01), and frame-walking counts that same trailing pad frame.

The chosen method replicates the ffmpeg mp3 demuxer exactly (source read: `libavformat/mp3dec.c` `mp3_parse_info_tag` / `mp3_parse_vbr_tags`):
- every Lavc/Lavf/LAME CBR stream declares its frames in the Xing/Info header **and** carries an encoder tag right after the header's flag-gated fields (frames/bytes/TOC/quality);
- tag+21 holds one 24-bit big-endian field: **delay = v>>12, padding = v&0xFFF**;
- the demuxer's duration is `(frames × spf − delay − padding) / sample_rate` (mp3dec.c:428-432), and playback trims `delay+529` samples at the start and `padding−529` at the tail (the ±529 is the MPEG decoder's own priming, which cancels — mp3dec.c:262-268), so the playable length is exactly `frames × spf − delay − padding` samples.

GMI's clips are Lavc-encoded 4/5/6/3 s silent-padded TTS narration: declared count **155/193/231/116** with pads **576+1584/576+1260/576+936/576+756** samples; the beds are Lavf-encoded 256 kbps stereo with the same structure. The fix reads the 24-bit word only when the tag's first four bytes are exactly `LAME`/`Lavf`/`Lavc`; an Info header with no encoder tag keeps the untrimmed declared count (pinned), and header-less CBR streams keep the byte-count fallback (every existing fixture unchanged).

Implementation: `mp3GaplessPads(b, payload, flags, end)` (new, pure Go) + `durationFromSamples(samples, sr)` (extracted from `mp3DurationFrom`), `u24` reader; declared × spf minus pads, `ErrClipDuration` when the pads exceed the declared frames. Files without a Xing/Info header are measured byte-for-byte as before.

## Per-clip validation (declared vs measured vs ffprobe)

Measured by the fixed `measureDuration` on the real files; ffprobe = `format=duration` (the reviewer's ground truth, cross-checked against decoded PCM byte counts — every integer-second clip decodes to exactly N×44100 samples). All 10 files carry Info + Lavc/Lavf tags.

| File | Info frames | pads (delay+tail) | declared product | **measured (fixed)** | ffprobe | delta |
|---|---|---|---|---|---|---|
| page-01.mp3 | 155 | 576+1584 | 4.048979591 s | **4.000000 s** | 4.000000 s | 0 |
| page-02.mp3 | 193 | 576+1260 | 5.041632653 s | **5.000000 s** | 5.000000 s | 0 |
| page-03.mp3 | 231 | 576+936 | 6.034285714 s | **6.000000 s** | 6.000000 s | 0 |
| page-04.mp3 | 116 | 576+756 | 3.030204081 s | **3.000000 s** | 3.000000 s | 0 |
| page-05.mp3 | 155 | 576+1584 | 4.048979591 s | **4.000000 s** | 4.000000 s | 0 |
| page-06.mp3 | 193 | 576+1260 | 5.041632653 s | **5.000000 s** | 5.000000 s | 0 |
| page-07.mp3 | 231 | 576+936 | 6.034285714 s | **6.000000 s** | 6.000000 s | 0 |
| page-08.mp3 | 116 | 576+756 | 3.030204081 s | **3.000000 s** | 3.000000 s | 0 |
| t12-music-bed.mp3 | 2166 | 576+1216 | 56.581224489 s | **56.540589569 s** | 56.540589569… s | <1 µs |
| t12-music-bed2-gibberish.mp3 | 4217 | 576+576 | 110.158367346 s | **110.132244897 s** | 110.132244897… s | <1 µs |

(Pre-fix overstatements — 30.20–48.98 ms per clip, 26–41 ms per bed — are exactly the round-1 reviewer's numbers. The sub-microsecond deltas on the beds are float64 floor-vs-round at the ns scale, not measurement error; every integer-second clip is exact because its trimmed sample count divides 44100.)

## End-to-end total re-check

Replica render of the 8 real cached clips through `bookvideo.Render` (default cards, real ffmpeg), page `Duration` = fixed measureDuration values:

| | Render total (fade anchor) | ffprobe of the film | delta |
|---|---|---|---|
| Round-1 (pre-fix, reviewer) | 42.810204078 s | 42.5232 s | **+0.287 s** — fade zero ~0.29 s AFTER film end |
| **Post-fix (this round)** | **42.500000 s** (3.5 cards + Σ36.0 playable + 3.0) | **42.523220 s** | **+23.22 ms** |

The residual 23.22 ms is one 1024-sample AAC frame of mux padding (1024/44100 = 23.219 ms) — the same sign and magnitude as the silent-tier control the reviewer measured (total 70.5000 s vs film 70.5232 s, "benign — fade completes just before the true end"). The wind-down now reaches silence ~23 ms **before** the true film end instead of 0.29 s after it: no residual bed level at film end, and `amix=duration=first` trims nothing audible. Requirement ("within ~0.1 s of the real film") met with 4× margin. Per-clip arithmetic is exact (measured == ffprobe on every clip), so any future render shape sums to the same anchor.

## Mutant demonstrations (re-applied; restores md5-verified)

| # | Mutation | Pin run | Result |
|---|---|---|---|
| **H1 revert** | `music.go`: drop `− startPad − endPad` (return the declared product) | `go test ./internal/audio -run 'TestMeasureDuration'` + real-file probe | **RED** — `TestMeasureDuration_InfoGaplessTagTrimsToPlayableLength` and `TestMeasureDuration_GaplessPadsExceedingFramesRejected` fail; the real-file probe fails all 10 files at 26–49 ms over ffprobe (the round-1 numbers). (mutant md5 `f8f1ada8…`; restored `ab1f74eb…` OK) |
| **MA6 (L1)** | `music.go`: Info frame count read at a shifted offset (`payload+9:payload+13`) | same pins | **RED** — all three `TestMeasureDuration_*` fixture tests fail (garbage frame counts → minute-scale durations); real-file probe fails 10/10. (mutant md5 `54f2f700…`; restored `ab1f74eb…` OK) |

Both mutants were also RED pre-restore against the real-file probe (page-01: H1-revert → 4.048979591 s; MA6 → 17m16.49 s). Restores byte-identical (md5 `ab1f74ebaa7c2ae34969b0eacbe34abf` on `internal/audio/music.go` before and after both runs).

## Committed pins (music_test.go)

- `infoFixture(declaredFrames, wantTag)` — mirrors page-01's parseable region: ID3v2.4 + mono MPEG-1 L3 first frame, REAL "Info" header (flags 0x0f), "Lavc63.1.101" tag with the 24-bit gapless word `0x24 0x06 0x30` at tag+21, then `declaredFrames−1` filler frames.
- `TestMeasureDuration_InfoGaplessTagTrimsToPlayableLength` — 155 declared frames, pads 576+1584 → exactly 4.000000 s (H1 pin; untrimmed declared product 4.04898 s would fail). Fixture self-guard asserts the declared product differs from the playable length.
- `TestMeasureDuration_InfoWithoutEncoderTagMeasuresDeclaredFrames` — a bare Info header keeps the untrimmed declared product (no-tag semantics preserved).
- `TestMeasureDuration_GaplessPadsExceedingFramesRejected` — corrupt gapless trim fails with `ErrClipDuration` instead of a negative length.

## Gates

Run in a HEAD worktree (`/tmp/thutapi-gate`, detached @ `69f0531`) with the T12 working files copied in — the shared tree's `internal/audio` test build was mid-flight broken by the sibling T13 lane's `clone_test.go` (compile error at line 85, then an assertion drifted 428→422 between two runs minutes apart — T13's voice-clone work is being edited live; the failing test is exclusively clone-scope, no T12 file involved). Audio coverage/race runs exclude T13's `clone.go` exactly as the round-1 reviewer's worktree did; `clone.go` IS present for the full-module build (cmd's dirty `main.go` references its symbols).

| Gate | Command | Result |
|---|---|---|
| Build | `go build ./...` (worktree, full module incl. cmd) | **PASS** (exit 0) |
| Vet | `go vet ./internal/audio` | **PASS** |
| Tests | `go test ./internal/audio ./internal/gmi/media ./internal/bookvideo ./internal/bookgen -race -count=1` | **PASS** (all four packages) |
| Coverage | `go test ./internal/audio -race -count=1 -cover` | **88.1 %** (floor 75 %; round-1 was 83.1 %) |
| Format | `gofmt -l internal/audio/music.go internal/audio/music_test.go` | clean (no output) |
| Shared tree spot-check | `go test ./internal/gmi/media ./internal/bookvideo ./internal/bookgen -count=1` | **PASS** — the shared tree is green in every package except T13's in-flight `clone_test.go` |

## Residue note

- H1's root was a misdescribing doc ("the measured length is the decoded length") — both the doc and the code are corrected.
- No behaviour changed outside the Info/Xing branch: the CBR fallback and WAV path are byte-identical in effect (all pre-existing fixture durations — `fixtureClipDuration`, narration pin `2.011428571 s` etc. — still pass unchanged), and an Info header without an encoder tag keeps the old untrimmed answer, so third-party Info files without gapless tags measure as before.
- Scratch probes (real-file validation, replica render) ran in the worktree only and were removed; the worktree was removed after gating; `/tmp` mutant backups removed. The shared tree was touched only by the three in-scope files.
- Not remediated here (out of seam): PLAN.md §T12's 3 s fade example text vs the 2 s default — orchestrator-owned doc note already recorded in round-1.
