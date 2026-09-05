# T10g round 1 — remediation

| | |
|---|---|
| **Target** | The two round-1 findings (F1 M, F2 L) of `t10g-round1.md` (0 C / 0 H / 1 M / 1 L → REMEDIATE). Verdict untouched: this file remediates, it does not re-verdict. |
| **Date** | 2026-09-06. Remediation agent `T10gRemediator`, fresh to the round. |
| **Status** | COMPLETE — F1 and F2 remediated; `go vet -tags live` clean on the migrated probe file; all scoped gates green. Zero residue is claimed only after round 2's verdict. |

---

## Dispositions

| # | Finding | Disposition | Status |
|---|---|---|---|
| 1 | **F1 (M)** — `internal/bookgen/live_test.go:1002-1004` (`TestLiveBookGeneration`, `//go:build live` — the T10e operator probe) still pins the OLD master geometry `s.Width != 1080 \|\| s.Height != 1350` → "want 1080x1350". T10g moved every segment to 1080×1620, so §T10g's mandated live geometry re-verification fails deterministically on the CORRECT output. | Assertion migrated to the current master geometry: `s.Width != 1080 \|\| s.Height != 1620` with the message "want 1080x1620" (live_test.go:1002-1003). The probe renders through the same real pipeline it always did (`NewFFmpegRenderer(bookvideo.Config{})`, live_test.go:645), so the operator's next live run now passes on correct output. The live probe itself was NOT run — see §Live re-run note. File still vet-compiles under `-tags live` (PASS, §Gates). | **DONE** — pin migrated; `go vet -tags live ./internal/bookgen` clean |
| 2 | **F2 (L)** — `internal/bookvideo/types.go:66-68` (exported `Config.FontFile` doc) reads "If empty, ffmpeg uses system fontconfig defaults" — false since T10g: empty FontFile materialises the embedded Fredoka-Regular instance and every drawtext carries an explicit `fontfile=`. Doc-only, but on the exported Config surface AGENTS.md §Go style protects. | Doc rewritten to match `materializeFont`'s actual behaviour exactly (types.go:66-71): empty FontFile → the package's embedded Fredoka-Regular instance is materialised into the render workdir and passed as `fontfile=` to every drawtext (the runtime image ships no fonts and no fontconfig, so there is no system fallback); an explicit path overrides. | **DONE** — doc matches `materializeFont` (video.go:45-60) |

---

## Evidence

### F1 — the old pin fails the correct 1080×1620 render (quote both)

The pin that was in the file before this remediation (live_test.go:1002-1003, `TestLiveBookGeneration`):

```go
if s.Width != 1080 || s.Height != 1350 {
	t.Errorf("video dimensions = %dx%d, want 1080x1350", s.Width, s.Height)
}
```

vs. the geometry every segment builder now emits — the single constant set that T10g introduced (caption.go:15-20), plus the card builders' shared master-geometry contract (video.go:103-106: "The card shares the master geometry (1080×1620) so it concat-copies with pages and the end card"):

```go
frameWidth  = 1080
frameHeight = 1620   // master frame: every segment renders at this
artWidth    = 1080
artHeight   = 1350   // 4:5 illustration, full bleed top
bandHeight  = frameHeight - artHeight // 270
```

Because the live probe renders through the same real pipeline as the pinned tests (`NewFFmpegRenderer(bookvideo.Config{})`, live_test.go:645 — verified by round-1 review), the probe's `s.Height` check compares a genuine 1620 against 1350 and fires `video dimensions = 1080x1620, want 1080x1350` on every frame — a deterministic failure on the output T10g now produces everywhere. That failure is precisely what §T10g's mandated re-verification ("T10e verified the pipeline live at 4:5. Changing the master geometry … re-run it", PLAN.md:2281-2283) would have hit. Post-fix assertion (live_test.go:1002-1003):

```go
if s.Width != 1080 || s.Height != 1620 {
	t.Errorf("video dimensions = %dx%d, want 1080x1620", s.Width, s.Height)
}
```

Real-pipeline confirmation (this remediation): `TestRender_RealFFmpeg` (bookvideo, not live-gated, self-skips without ffmpeg) renders title + captioned pages + end card through real ffmpeg and probes `h264,1080,1620` — **PASS** on this box. The migrated pin asserts exactly what the real pipeline emits. `go vet -tags live ./internal/bookgen` **PASS** after the edit.

### F2 — the doc-vs-code mismatch (quote both)

The doc that was in the file before this remediation (types.go:66-68):

```go
// FontFile is an optional path to a TTF or OTF font file for drawtext filters.
// If empty, ffmpeg uses system fontconfig defaults.
FontFile string
```

vs. what `materializeFont` actually does since T10g (video.go:45-60) — empty FontFile materialises the embedded face, and the option builder makes `fontfile=` unconditional:

```go
// materializeFont returns a path to a drawtext font file: cfg.FontFile when
// set, otherwise the embedded Fredoka Regular instance written into dir. The
// runtime image ships no fonts and no fontconfig config, so every drawtext
// needs an explicit fontfile (verified in-container); the embedded face is
// the film's caption/card face (§T10g D4).
func materializeFont(cfg Config, dir string) (fontPath string, cleanup func(), err error) {
	if cfg.FontFile != "" {
		return cfg.FontFile, nil, nil
	}
	p, err := writeTempFile(dir, "fredoka-*.ttf", fredokaRegularTTF)
	...
}
```

`buildFontOpt` (video.go:62-66) then prepends `fontfile=` (filter-path-escaped) to **every** drawtext — title, byline, caption and end card. "System fontconfig defaults" described the pre-T10g behaviour (fontOpt emitted no `fontfile=` when empty); since T10g the field's empty default is the embedded Fredoka-Regular instance written into the render workdir, and the container has no fontconfig for drawtext to fall back to anyway (T10g D4). Post-fix doc (types.go:66-71):

```go
// FontFile is an optional path to a TTF or OTF font file for drawtext filters.
// If empty, the package's embedded Fredoka-Regular instance is materialised
// into the render workdir and passed as fontfile= to every drawtext — the
// runtime image ships no fonts and no fontconfig config, so there is no
// system fallback (§T10g D4). An explicit path overrides the embedded
// default.
FontFile string
```

---

## Live re-run note

The migrated probe (`go test -tags live -run TestLiveBookGeneration -v -timeout 25m ./internal/bookgen/`) was **not** executed: it is the T10e operator step — real services, real money on the paid paths (structure/illustrate/narrate), real ffmpeg 7.1 in the container, and it is exactly the re-verification §T10g mandates after the master-geometry change. This remediation migrates the assertion that was blocking it; the operator re-runs the probe at T10e's geometry re-verification, and it now passes on the correct 1080×1620 output instead of failing on it.

---

## Gates

All gates run from the remediated tree (scoped to the touched packages; project-wide validation is the orchestrator's job once sibling lanes land):

| Gate | Command | Result |
|---|---|---|
| Format (touched) | `gofmt -l internal/bookgen/live_test.go internal/bookvideo/types.go` | **PASS** — 0 files |
| Format (tree) | `gofmt -l .` | **FAIL (external)** — `internal/interview/http.go`, `internal/interview/interview.go`: the T9 lane's own mid-flight files (the same two round-1 observed); neither file touched here |
| Vet | `go vet ./internal/bookgen ./internal/bookvideo` | **PASS** |
| Vet (live tag) | `go vet -tags live ./internal/bookgen` | **PASS** — the migrated probe file still vet-compiles under `-tags live` |
| Build | `go build ./...` | **PASS** |
| Race | `go test ./internal/bookvideo ./internal/bookgen -race -count=1` | **PASS** — bookvideo ok, bookgen ok |
| Real-ffmpeg smoke | `go test ./internal/bookvideo -run TestRender_RealFFmpeg -v` | **PASS** — probed `h264,1080,1620`: the real pipeline emits the geometry the migrated assertion pins |

---

## Files changed

| File | Change | Final md5 |
|---|---|---|
| `internal/bookgen/live_test.go` | F1: geometry assertion migrated `1080x1350` → `1080x1620` (lines 1002-1003), message "want 1080x1620" | `1610b1f4…` (was `cb5f3f91…`) |
| `internal/bookvideo/types.go` | F2: `Config.FontFile` doc rewritten to match `materializeFont` (lines 66-71) | `2b4bcf64…` (was `1547f7b5…`) |
| `dev-diary/adversarial-review/t10g-remediation-round1.md` | this record | — |

**Touch scope respected: exactly these three files.** `internal/bookgen/live_test.go`, `internal/bookvideo/types.go`, and this record — nothing else. The T9 lane's uncommitted work (internal/interview/**, static/**, internal/web/**, cmd/thutapi/main.go, t9-*.md) was not touched, staged or committed.

---

## Residue note

- **F1 and F2 remediated** — the only two round-1 findings. No severity left open.
- **The live re-run is the operator's T10e step**, not a remediation action: the assertion migration is complete and vet-verified; executing the probe is the §T10g-mandated operator re-verification (see §Live re-run note).
- **The tree-wide `gofmt -l .` failure is external** — `internal/interview/http.go`, `internal/interview/interview.go` are the T9 lane's own mid-flight files, byte-untouched here.
- Per AGENTS.md, the remediation agent does not alter the review verdict. Round 2 must independently audit the remediation and confirm zero residue before issuing an APPROVE.
