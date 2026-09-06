# T12 round 1 — implementation record (stub for adversarial review)

**Track:** T12 — music bed (PLAN.md §T12).
**Agent:** T12Implementer. **Base:** `main` @ `69f0531`.
**Date:** 2026-09-06.
**Status:** implemented; local suites green in a HEAD worktree (see §Gates). Awaiting adversarial review.

---

## Settled facts — operator live probes 2026-09-06 (evidence, NOT re-probed)

Recorded here as the batch's binding evidence; no live call was made by this implementation.

- **Model:** `minimax-music-3.0` on the request queue (console.gmicloud.ai). Synchronous (~60–110 s), result at `outcome.media_urls[].url` + `outcome.duration_ms`; the single saved terminal record (`data/live/t12-music-record.json`, bed 1) carries `media_urls[0].url` **beside** `outcome.audio_url` and `duration_ms: 56581`.
- **Payload:** `lyrics` REQUIRED (1–3500 chars), `prompt` optional style, `format` mp3 (also wav/pcm), optional `sample_rate`/`bitrate`. **No duration parameter.**
- **Instrumental-directing lyrics do NOT yield instrumental** — the model sings real words. The **operator-ear-verified usable shape** = gibberish-vocalise lyrics ("La la la / Doo doo / Mm mm" lines, no language) + an explicit "wordless vocalise, no real words, no language, instrumental with soft humming" prompt → a wordless bed. Two probe beds: `data/live/t12-music-bed.mp3` (56.6 s, sung — the failure), `data/live/t12-music-bed2-gibberish.mp3` (110.2 s, wordless — the keeper).
- **Operator requirement (binding):** the bed winds down to the FILM's duration — trimmed when longer, looped when shorter, end fade reaching silence exactly at film end.
- **Operator directive (binding, 2026-09-06, via Main):** the film's total is KNOWN BEFORE the mix, computed by the render phase's own arithmetic — **never ffprobe of the finished file**. Terms: title/end card fixed holds; silent pages = words/2.0 s clamp [4 s,14 s]; **narrated pages = their clips' durations, which must be made Go-known** (the GMI TTS outcome carries no duration — `t2b-t5b-live-record.md`'s terminal TTS envelope is `outcome {audio_url, format, status}` — and the runtime image ships **no ffprobe**; Dockerfile copies only `/ffmpeg`). Main's correction confirmed the narrated term was NOT computable in Go before this track (PageInput had no duration; nothing measured clips).
- Local evidence taken for this implementation (no GMI): the mix command validated against real ffmpeg — 12 s silent film, 5 s and 20 s beds, fade st=10 d=2: output 12.000000 s, bed full until 10 s, linear ramp to silence at exactly 12 s, both cases; and the pure-Go duration reader validated on the real cached GMI narration clips (`data/live/cache/audio/page-01/04/08.mp3`: Info-header 155/116/116 frames = 4.049 s / 3.030 s — matching ffprobe's own decoded frame counts) and on the 110.2 s operator bed (110.158 s measured).

## Decisions (evidence and rationale)

### D1 — where the bed-fit mix runs and what replaces what
The mix is **bookgen stage 5's tail** (`renderFilm`), after `Video.Render` produced the finished film and BEFORE the persist: a bed is generated (`audio.GenerateMusicBed`), mixed under the finished film (`audio.MixBed`), and the **mixed film is persisted as the book's one `video/mp4` row** — the bed film *replaces* the plain one (answers PLAN.md §T12's open question: one film slot per book; `supersedeFilms` unchanged). Music **off** (Config.Music nil) = today's exact behaviour, film untouched. Transient music failure (`gmi.ErrTransient`) degrades to the no-music film with a warn — mirroring the narration-outage path; any other music failure fails the run.

### D2 — the mix is the operator-verified single final pass, fade anchored to the KNOWN total
`MixBed` takes `FilmDuration` (the render's computed total) and emits the verified command: `-stream_loop -1` (shorter bed loops; longer is trimmed by `amix=duration=first`), `[1:a]volume=0.15,afade=t=out:st=<total-fade>:d=<fade>[bed];[0:a][bed]amix=inputs=2:duration=first:normalize=0[a]`, `-map 0:v -c:v copy -c:a aac -b:a 128k -movflags +faststart`. Defaults: gain 0.15 (PLAN §T12), fade **2 s** (operator directive 2026-09-06 "start the fade ~2 s before the end"; supersedes the plan's 3 s — configurable). The fade ends exactly at the total in both the longer-bed and shorter-bed cases. Real-ffmpeg evidence: output duration preserved byte-for-byte; windowed peak analysis shows full bed level through the body and near-silence at the end for both cases.

### D3 — the narrated term: pure-Go clip measurement at narrate time (ffprobe-free)
No ffprobe in the runtime image; the TTS envelope carries no duration. `measureDuration` (music.go) reads a clip's length from its own bytes in pure Go: WAV via fmt/data chunks (exact); MP3 via ID3v2 syncsafe skip (+ID3v1 strip), first-frame header, then the **Xing/Info frame count** (flags at +4, frames at +8 when `flags&1` — GMI's clips carry "Info" 0x0f, verified frame counts match ffprobe's decode) or a CBR byte-count estimate otherwise. Accurate to one MPEG frame (~26 ms) — far inside a 2 s fade's tolerance. `audio.NarrateBook` measures each clip as it downloads (narratePage) and carries it on `Clip.Duration` → `renderFilm` puts it on `PageInput.Duration` → `bookvideo.Render` sums it. **Rejected:** ffprobe anywhere in the film path (absent from the image; operator directive). A narration page without a Go-known Duration is refused loudly (the total would be uncomputable); a Duration on a silent page is equally refused.

### D4 — where the total is computed
`bookvideo.Render` returns `(time.Duration, error)`: resolved title card hold + Σ(page holds) + resolved end card hold; a page's hold is its `PageInput.Duration` (narrated) or `captionHold(text)` (silent). All terms Go-known before the mix; the mix never touches the finished file's duration. The `videoRenderer` seam returns the total so `renderFilm` threads it to `MixBed`.

### D5 — defaults pin the settled operator-verified wire shape
`DefaultMusicLyrics` (gibberish vocalise lines) + `DefaultMusicPrompt` ("wordless vocalise, no real words, no language, instrumental with soft humming …") are sent when `MusicConfig` overrides are empty. Changing either half is a wire change needing live re-verification (pinned at the seam and at the raw wire in the media client).

## Contract rows (file:line:change:reason)

| # | Where (post-edit) | Change | Reason |
| --- | --- | --- | --- |
| C1 | `internal/gmi/media/client.go` SynthesizeMusic + musicPayload + defaults | New typed method `SynthesizeMusic(ctx, lyrics, prompt, model) ([]byte, error)` posting `{model:"minimax-music-3.0", payload:{lyrics, prompt(omitempty), format:"mp3"}}` with `Accept: audio/*` | Invariant 1: the music POST goes through the request-queue client; T8-C1 precedent. Pinned raw-wire in `client_test.go`. |
| C2 | `internal/audio/audio.go` Clip + narratePage; `internal/audio/music.go` measureDuration/ErrClipDuration | `Clip.Duration`; narratePage measures each downloaded clip in pure Go (ErrClipDuration when unreadable); narration fixtures become parseable MP3s | T8 is closed; the narrated film total needs each clip's Go-known duration (no ffprobe in the image; TTS envelope has none). |
| C3 | `internal/bookvideo/types.go` PageInput.Duration; `internal/bookvideo/video.go` Render | `PageInput.Duration` (required iff narration present); `Render` returns the computed total `(time.Duration, error)`; loud validation of the duration pair | The render phase's arithmetic is the only legal source of the film's total; the mix anchors to it. |
| C4 | `internal/bookgen/bookgen.go` videoRenderer + Config.Music/MusicMix; `internal/bookgen/video.go`; `internal/bookgen/pipeline.go` renderFilm + mixFilm | Seam `Render → (time.Duration, error)`; optional `Config.Music audio.Music` (nil = no music) + `MusicMix audio.MixConfig`; stage 5 generates the bed, mixes under the film pre-persist (transient degrades to no-music film; other failures total); mixed film replaces the plain video row | The mix is the pipeline's opt-in final pass; tests inject scripted music/mix seams so no test needs ffmpeg or GMI. |

## Files
- New: `internal/audio/music.go` (bed gen/decode/download, MixBed, measureDuration), `internal/audio/music_test.go`, `dev-diary/adversarial-review/t12-round1.md`.
- Edited: `internal/gmi/media/client.go`+`client_test.go` (C1); `internal/audio/audio.go`+`narrate_test.go`+`audio_test.go`+`wire_test.go` (C2); `internal/bookvideo/types.go`+`video.go`+`bookvideo_test.go` (C3); `internal/bookgen/bookgen.go`+`video.go`+`pipeline.go`+`harness_test.go`+`pipeline_test.go`+`live_test.go` (C4).
- Untouched by design: PLAN.md, cmd/ wiring (production enablement = passing `mediaCli` as `Config.Music` is one line in cmd's `run()` — left for the orchestrator/operator decision, outside this track's seam). Sibling T13 untracked work (`internal/audio/clone.go`/`clone_test.go`) left byte-identical (md5-verified).

## Tests added (behavioural pins)
- Media: `TestSynthesizeMusic*` raw-wire payload/model/Accept pins + empty-lyrics refusal.
- Audio: defaults reach the seam; decode keys on outcome.media_urls (audio_url fallback) + duration; ErrNoBed branches; download errors; mix command pinned with fade end == total (longer/short-bed cases, one command); ErrInvalidMix/ErrMixFailed branches; real-ffmpeg wind-down (looped short bed + trimmed long bed reach near-silence at the total, duration preserved); Clip.Duration carried from NarrateBook; ErrClipDuration on unreadable clips; `var _ Music = (*media.Client)(nil)`.
- Bookvideo: Render returns title+clip-duration+captionHold+end totals (mock + real ffmpeg); narration-without-Duration and Duration-without-narration refusals.
- Bookgen: music-on mixes render output with the renderer's total and persists the MIXED row; music-off unchanged; transient bed failure degrades to the plain film (book_ready, no mix); hard bed failure and mix failure fail the run with no film row; renderer input carries each clip's Go-known Duration.

## Gates (run in the HEAD worktree — sibling clone work is excluded there per the shared-tree rule; the shared tree itself was kept byte-identical except my edits)
- `go test ./internal/audio ./internal/gmi/media ./internal/bookvideo ./internal/bookgen -count=1` — **green** (incl. real-ffmpeg guarded tests; T8/T10 suites intact under the fixture change).
- `go build ./...`, `go vet ./...` (touched packages), `gofmt -l` on my files — clean in the main tree; `go vet -tags live ./internal/bookgen` clean.
- NOT yet run by me (Main runs project-wide gates after all subagents land): full `go test ./... -race -count=1`, `-count=5` per touched package, coverage floors, and the live-tag build with siblings' files in place.

## Verification evidence for the reviewer
- Real-ffmpeg mix validation (D2): `/tmp/t12mix` + `/tmp/t12re` outputs — 12.000000 s preserved; per-0.5 s bin peaks 416 → 93 → 0 across the fade; identical for looped 5 s and trimmed 20 s beds.
- Real-clip duration reader (D3): GMI narration clips measure 4.04898 s (155 Info frames), 3.03020 s ×2 (116 frames); operator bed2 110.158 s — all matching ffprobe's own decoded frame counts within one frame.

---

# T12 round 1 — adversarial review (fresh reviewer)

| | |
|---|---|
| **Target** | Track T12 — music bed (PLAN.md §T12), base `main` @ `69f0531` + the T12 working tree: `internal/audio/music.go` (+`music_test.go`), `internal/audio/{audio,audio_test,narrate_test,wire_test}.go`, `internal/gmi/media/client.go`(+test) SynthesizeMusic, `internal/bookvideo/{types,video,bookvideo_test}.go`, `internal/bookgen/{bookgen,video,pipeline,harness_test,pipeline_test,live_test}.go`. |
| **Evidence** | Stub above (contract rows C1–C4, decisions D1–D5, settled facts); PLAN.md §T12 (2459–2533), §T10g (2222–2369), §T12 row (233); the four T12 packages read in full; diffs vs HEAD of every touched file; real-clip probes against `data/live/cache/audio/page-0N.mp3` + `data/live/t12-music-*.mp3` + `t12-music-record.json`; full gate battery; byte-identical mutation re-runs (md5-verified). No live GMI calls. |
| **Method** | Fresh reviewer with **no prior T12 round** — probe, don't trust prose. Reviewer probes were run as throwaway tests in a HEAD worktree plus /tmp scratch renders and mixes (all removed; the shared tree was touched only by the byte-identical mutant re-runs below). READ-ONLY except this file. |
| **Reviewer / Date** | `T12Round1Reviewer`, 2026-09-06. Shared-tree note: sibling lanes (T13 + others) hold uncommitted files (`cmd/**`, `static/**`, `.claude/`, `internal/audio/clone.go`, `internal/gmi/media/client.go` CloneVoice addition, interview, README/deploy edits); the tree evolved **during** this review — the recorded cmd cutover debt (stale `failRenderer` in T13's dirty `cmd/thutapi/main_test.go` against the C3 seam change) was **already migrated by the sibling lane before this review ran**: `failRenderer.Render` now returns `(time.Duration, error)` (main_test.go:130-131), `main.go` wires `Music: mediaCli` (:349), and the cmd suite builds and runs. Nothing in the external lanes' files is counted against T12. |
| **Verdict** | **REMEDIATE — 0 × C, 1 × H, 0 × M, 1 × L** |

## Severity counts

**0 C / 1 H / 0 M / 1 L**

## Findings table

| # | Sev | Where | What | Pin | Mutation |
|---|---|---|---|---|---|
| **H1** | **H** | `internal/audio/music.go:648-660` (`measureDuration` Xing/Info branch) → `audio.Clip.Duration` → `bookvideo.Render` total → `MixBed` fade anchor | The Info/Xing frame-count path **overstates real GMI narration clips by ~1.2–1.9 MPEG frames (~30–49 ms per clip)**. Real cached clips (Lavf 63.1.101, all with `Info` headers) declare 155/116/193 frames → Go measures 4.04898 s / 3.03020 s / 5.04163 s, but the same ffmpeg binary plays exactly 4.000 s / 3.000 s / 5.000 s (decoded PCM byte count, ffprobe stream+format duration, and a full `BuildPageSegment`-replica render all agree); the declared count includes ~1.5 unplayable trailing frames. The recorded validation ("matching ffprobe's own decoded frame counts") compared against ffprobe's *frame-count* view, which reads the same Xing count — not the playable length that governs `-shortest` page holds. Consequence (measured end to end): an 8-page narrated book renders to **42.523 s** while the render-computed total (what the mix anchors to) is **42.810 s** (+0.287 s, ~36 ms/page). `MixBed`'s 2 s end fade therefore reaches zero ~0.29 s *after* the film ends; `amix=duration=first` cuts the bed at ~6–14 % residual (−17…−24 dB rel. bed; measured tail peak 138 vs 26 when anchored at the true end, on a real 42.5 s film mixed with the real 110 s keeper bed) instead of silence exactly at film end — violating the operator's binding wind-down requirement on the flagship narrated tier (silent tier is unaffected: measured +0.023 s, benign direction). | Reviewer probe (runnable against `data/live`): 8-page real-ffmpeg render of the real cached clips through T12's `Render` — assert `|ffprobe(dur) − total| < 0.1 s` → **FAILS**: 42.5232 vs 42.8102 s. Single-page replica: page-01 clip holds exactly 4.000000 s, Go duration 4.048979591 s. | Correct the measurement/anchoring so the narrated tier's fade reaches silence at the true film end (e.g. reconcile the Info count with the playable decode — subtract the ~1.5-frame tail the Lavf files carry — and pin the page-hold ≡ measured-length equivalence on a real-shape fixture); re-anchor today's code (trust the raw Info count) reproduces the +0.287 s gap. |
| **L1** | **L** | `internal/audio/music.go:648-660` + fixture (`audio_test.go` `makeMP3` builds ID3 + CBR frames, **no Info/Xing header**) | The **Xing/Info branch is unpinned by the committed suite** — the fixtures exercise only the CBR-byte-count fallback, a path real GMI clips never take (all 8 cached clips + both operator beds carry `Info`). This is the branch where H1 lives, and it let H1 through the entire green suite. | **Mutant MA6 re-applied by me**: Xing frame count read at a shifted offset (`u32(b[payload+9:payload+13])`) → `go test ./internal/audio -count=1` **GREEN** (full suite). Restored byte-identical (music.go md5 `4f8d66f2…` OK). | Add a fixture whose first frame carries a real `Info` header (declared count ≠ playable decode, truncated tail) and pin `measureDuration` against the decoded length; revert to today's fixtures reproduces the blind spot. |

## Contract rows C1–C4 — rulings

| Row | Ruling | Evidence |
|---|---|---|
| **C1** (media SynthesizeMusic — invariant 1 + raw wire) | **PASS** | `SynthesizeMusic(ctx, lyrics, prompt, model)` posts the request-queue envelope `{model:"minimax-music-3.0", payload:{lyrics, prompt(omitempty), format:"mp3"}}` with `Accept: audio/*` (client.go:376-389); `musicPayload` is a named struct, never a map; empty lyrics refused pre-flight; defaults pinned on the wire by `TestSynthesizeMusic` + `TestSynthesizeMusic_RawWirePinned` (byte-exact body with the prompt key omitted) + `TestSynthesizeMusic_EmptyLyricsRejected`; `var _ Music = (*media.Client)(nil)` in audio/wire_test.go pins the seam at compile time. Decode keys on `outcome.media_urls[0].url` (audio_url fallback) + `duration_ms` — never the payload echo (T6-H1 hazard) — pinned by `TestDecodeBedURL_KeysOnOutcome` with a decoy payload URL. |
| **C2** (audio Clip.Duration + pure-Go measureDuration) | **DEFECT (H1)** | `narratePage` measures each downloaded clip before persisting and carries `Clip.Duration`; `ErrClipDuration` on unreadable bytes, nothing persisted on failure (pinned by `TestNarrateBook_UnmeasurableClipFailsLoudly` + `TestNarrateBook_DefaultsReachTheCalls`); reader structure is sound (ID3v2 syncsafe skip + footer, ID3v1 strip, frame sync scan, side-info offsets, spf/bitrate/sample-rate tables, word-aligned WAV chunks). **But the Info/Xing branch overstates the real clips' playable length by ~1.2–1.9 frames (~30–49 ms/clip)** — measured against the actual files and against ffmpeg's own decode (see H1). The WAV branch and the CBR fallback measure exactly; the recorded "matching ffprobe's decoded frame counts" validation is true only at the frame-count level, not at the playable-length level that governs the film. Bed measurement (110.158 s vs ffprobe 110.132 s, +26 ms ≈ 1 frame) is fine — `Bed.Duration` is informational and never anchors the mix. |
| **C3** (bookvideo PageInput.Duration required-iff-narration; Render returns the computed total) | **PASS with H1 note** | `Render` now returns `(time.Duration, error)`; the total is pure Go arithmetic — `resolved.TitleCardDuration + Σ(audio-page Duration | silent captionHold) + resolved.EndCardDuration` (video.go:574-586) — never ffprobe; narrated-page-without-Duration and Duration-without-narration both refused loudly at validation (pinned in `TestErrorSentinels`, :612-635); the returned total equals the exact card+clip arithmetic in both the mock and the real-ffmpeg suites (`TestRender_EndToEnd_Mock`, `TestRender_RealFFmpeg`). **H1 note**: nothing in the suite pins the *underlying assumption* that the narrated film's actual duration equals the computed total — the real-ffmpeg test checks geometry/tags, not duration-vs-total, and the assumption fails by ~36 ms/page on real clips (H1). |
| **C4** (bookgen Music seam + opt-in mix + transient degrade + hard-fail) | **PASS with H1 note** | `Config.Music` nil = plain film, unchanged (pinned by every music-off pipeline test); non-nil → stage 5 generates the bed (`audio.GenerateMusicBed` — default gibberish-vocalise lyrics + wordless prompt pinned reaching the seam), mixes via `audio.MixBed` with the renderer's computed total as `FilmDuration` (fade `st=total−2s` asserted in the recorded command), persists the **mixed** film as the book's one video row (supersede unchanged); `gmi.ErrTransient` bed failure degrades to the plain film with a warn + `book_ready`; any other bed failure and any mix failure fail the run with **no** film row. All four semantics pinned end to end (`TestPipeline_MusicOnMixesTheBedUnderTheFilm`, `…_MusicTransientFailureDegradesToThePlainFilm`, `…_MusicHardFailureFailsRun`, `…_MixFailureFailsRun`; see the mutation table for the re-applied mutants). The mix anchors to the renderer's total by construction — which is exactly why H1's overstatement propagates into the wind-down. |

## The mix wind-down (operator requirement) — verified

`MixBed` emits the operator-verified single final pass (music.go:472-524): `-i film -stream_loop -1 -i bed -filter_complex "[1:a]volume=<gain>,afade=t=out:st=<total−fade>:d=<fade>[bed];[0:a][bed]amix=inputs=2:duration=first:normalize=0[a]" -map 0:v -c:v copy -c:a aac -b:a 128k -movflags +faststart`. Defaults 0.15 gain / 2 s fade (operator directive 2026-09-06; PLAN.md §T12's 3 s example text was not updated — orchestrator-owned doc, noted only). The fade **end == known total** is genuinely pinned, not just string-pinned:
- `TestMixBed_CommandPinned_FadeEndsAtFilmTotal` asserts the exact filter and re-parses `st`/`d` to assert `st+d == total` (±1 ms);
- `TestMixBed_RealFFmpeg_WindDown` (real ffmpeg, this machine) passes both directions — a 2 s bed looped under a 6 s film and a 12 s bed trimmed at 6 s both keep the output at 6.000 s, run at full bed level through the body, and reach near-silence in the final window (windowed-peak assertions);
- my own real-film mix (42.523 s film + the real 110.2 s keeper bed) with the prod anchor holds duration 42.523220 s and reaches a **tail peak 138** in [42.45, 42.52] vs **26** when the fade is anchored at the film's true end — i.e. the mechanism works exactly as specified *given an accurate total*, which is precisely where H1 breaks it on narrated books.
- Mutants MA1 (fade not anchored to total) and MA2 (`duration=first` dropped — would also make the short-bed case hang on an endless looped input) both go **RED** on the pins.

## Duration measurement — verified against the real files

Probe results (real cached GMI narration clips `page-01..08.mp3`, operator beds; measured = T12 `measureDuration`, decoded = ffmpeg PCM byte count / ffprobe stream duration; all clips carry `Info` headers):

| File | Info frames | Measured (Go) | ffprobe / decoded | Delta |
|---|---|---|---|---|
| page-01.mp3 | 155 | 4.048979591 s | 4.000000 s | **+48.98 ms** |
| page-02.mp3 | 193 | 5.041632653 s | 5.000000 s | **+41.63 ms** |
| page-03.mp3 | 233 | 6.034285714 s | 6.000000 s | **+34.29 ms** |
| page-04.mp3 | 116 | 3.030204081 s | 3.000000 s | **+30.20 ms** |
| page-08.mp3 | 116 | 3.030204081 s | 3.000000 s | **+30.20 ms** |
| t12-music-bed.mp3 | — | 56.581224489 s | 56.5406 s | +40.63 ms (informational) |
| t12-music-bed2-gibberish.mp3 | — | 110.158367346 s | 110.1322 s | +26.12 ms (informational) |

Single-page render replica of `BuildPageSegment` with the real page-01/page-04 clips: output **4.000000 s / 3.000000 s** — the page holds the *playable* length, not the Go-measured length. Full 8-page real render: `Render` total **42.810204078 s** vs actual file **42.5232 s** (+0.287 s). Silent-tier control (8 silent pages): total 70.5000 s vs actual 70.5232 s (−0.023 s, benign — fade completes just before the true end). The recorded 155/116-count validation is confirmed at the frame-count level and **disconfirmed at the playable-length level** (H1).

## Mutation testing (re-applied by me; backups in /tmp, md5-verified byte-identical restores)

| # | Mutation | Pin run | Result |
|---|---|---|---|
| MA1 | `music.go`: fade start `FilmDuration−fade` → `fade` (fade 0→2 s, not anchored to total) | `go test ./internal/audio -run TestMixBed` | **RED** — `TestMixBed_RealFFmpeg_WindDown` both subtests: "the fade must run over the LAST two seconds"; command pin red. (mutant md5 `0667cdd9…`; restored `4f8d66f2…` OK) |
| MA2 | `music.go`: drop `duration=first` from amix | `go test ./internal/audio -run 'TestMixBed_CommandPinned_FadeEndsAtFilmTotal\|TestMixBed_CustomGainAndFade'` | **RED** — byte-for-byte filter pins (short-bed real-ffmpeg case would hang on the endlessly looped bed rather than fail; the command pins fail first). (mutant md5 `691a650a…`; restored OK) |
| MA3 | `pipeline.go`: music step runs even when `Config.Music` is nil (guard removed) | `go test ./internal/bookgen -run 'TestPipeline_EndToEnd\|TestPipeline_NarrationTransientOutageProducesCaptionedSilentFilm'` | **RED** — `event = "failed" ({}), want "book_ready"`: music-off runs must stay plain-film runs. (mutant md5 `a8163ae6…`; restored `bcad73a7…` OK) |
| MA4 | `music.go`: MPEG-1 sample-rate table scrambled (44100↔32000) — measureDuration mis-parse | `go test ./internal/audio -run TestNarrateBook_DefaultsReachTheCalls` | **RED** — `clip 1 duration = 1.98s, want 2.011428571s`. (mutant md5 `0b716fc3…`; restored OK) |
| MA5 | `audio.go`: measured but `Clip.Duration` not carried (zero) | same pin | **RED** — `clip 1 duration = 0s, want 2.011428571s`. (mutant md5 `b2095a7c…`; restored `8095ed1d…` OK) |
| MA6 | `music.go`: Xing/Info frame count read at a shifted offset (`payload+9:payload+13`) | `go test ./internal/audio -count=1` (full suite) | **GREEN** — the Xing/Info branch is unpinned (L1). (mutant md5 `c52fc97c…`; restored OK) |

## Verification gates

| Gate | Command | Result |
|---|---|---|
| Build | `go build ./...` | **PASS** (exit 0, final snapshot) |
| Vet (T12 packages) | `go vet ./internal/audio ./internal/gmi/media ./internal/bookvideo ./internal/bookgen` | **PASS** (exit 0, final snapshot; a mid-review failure was T13's transiently-broken `clone_test.go` — external, fixed by the sibling lane) |
| Format | `gofmt -l` on all 18 T12 files | **PASS** (no output) |
| Tests + coverage, audio | `go test ./internal/audio -race -count=1 -cover` | **PASS** — 83.1 % (floor 75 %); real-ffmpeg wind-down ran, not skipped |
| Tests + coverage, gmi/media | `go test ./internal/gmi/media -race -count=1 -cover` | **PASS** — 92.3 % |
| Tests + coverage, bookvideo | `go test ./internal/bookvideo -race -count=1 -cover` | **PASS** — 88.4 % (real-ffmpeg render suite ran) |
| Tests + coverage, bookgen | `go test ./internal/bookgen -race -count=1 -cover` | **PASS** — 82.1 % |
| Workspace | `go test ./... -race -count=1` | Mid-review snapshot: **PASS** (all packages ok). Final snapshot: **cmd/thutapi** `TestShutdownLogRecordsSignalName` — data race on a shared `bytes.Buffer` between the test reader and `run()`'s signal goroutine (cmd is T13-owned dirty; outside T12's delta, passed earlier under identical flags — flaky/churn), and **internal/interview** `TestRestartedHandlerSeesEndedInterview` — 5.1 s timeout (outside T12's delta). Neither touches a T12 file; attributed external. |

**Recorded cutover debt — status.** The cmd `failRenderer` migration against the C3 seam was confirmed **already landed by the sibling lane** before this review (main_test.go:130-131 returns `(time.Duration, error)`; main.go:349 `Music: mediaCli`); cmd builds and tests green at the mid-review snapshot. No remaining T12-external breakage was attributable to T12 at review time.

## Zero-residue claim

Round 1 — no prior T12 rounds to claim residue against. All probes, scratch tests and tmp artifacts removed; the shared tree is byte-identical to the review start except this file (T12 file md5s verified: music.go `4f8d66f2…`, audio.go `8095ed1d…`, pipeline.go `bcad73a7…`, bookgen.go `353c6300…`, video.go `fa38d3ef…`, audio_test `ec7d2990…`, pipeline_test `ce7ec68c…`, harness_test `4308ffcd…`; client.go differs only by the sibling lane's CloneVoice addition).

**Verdict: REMEDIATE — 0 C / 1 H / 0 M / 1 L** (H1: Info/Xing duration overstatement breaks the narrated-tier wind-down anchor; L1: Xing/Info branch unpinned).
