# T9a round 2 — adversarial review of the round-1 remediation

| | |
|---|---|
| **Target** | Track T9a round-1 remediation (`t9a-remediation-round1.md`), covering the five findings of `t9a-round1.md` (0 C / 1 H / 2 M / 2 L) across `static/race/**` (`race.html`, `index.html`, `race_test.go`). |
| **Evidence** | `AGENTS.md` in full; `dev-diary/PLAN.md` §T9a ("The race", "Done when"); `dev-diary/prototypes/race.html`; `t9a-round1.md` (the round 1 review); `t9a-remediation-round1.md` (the round 1 remediation record); `go test ./static/race/... -v -race` (all 8 tests PASS); `go test ./... -race` (clean repo-wide); `go vet ./...` (clean); `gofmt -l .` (clean); `diff -u static/race/race.html static/race/index.html` (clean, byte-identical); container query geometry analysis (390px mobile and 900px desktop) across `--done` 0..8; cubic-bezier curve monotonicity and bound analysis; composite-only transform and `prefers-reduced-motion` compliance audit. |
| **Method** | Fresh-agent adversarial audit for Round 2. Trust neither round 1 assertions nor remediation claims. Re-read all diffs and source files. Re-audit each finding (H1, M1, M2, L1, L2) against the code, tests, and mutants. Verify SVG ear headroom and overflow visibility. Verify composite-only transitions on `.markers::before`. Verify runner cubic-bezier curve monotonicity and container clearance at `--done: 8`. Verify full tokenization of SVG colors with fallback values. Verify test pins and regression resilience. Verify absence of scope creep or unowned file edits. |
| **Date** | 2026-09-05. Reviewer: fresh adversarial review agent (not the implementer, not the round 1 reviewer, not the round 1 remediation agent). |

**Verdict: APPROVE — 0 × C, 0 × H, 0 × M, 0 × L (zero residue).**

---

## Summary of Round-1 Remediation Audit

Round 1 identified 5 findings across SVG clipping, layout property animation, spring transition overshoot, raw color literals, and test suite verification gaps (0 C / 1 H / 2 M / 2 L). All 5 findings have been thoroughly remediated and backed by automated test pins:

1. **H1 (SVG ear tips hard-clipped during `.bob` animation):**
   - In `static/race/race.html:123` and `static/race/index.html:123`, added `.runner svg{overflow:visible}`.
   - Because `.lane` has `height: 52px` and `.runner` has `top: 4px; height: 44px;`, 4px of headroom is provided above the runner inside each lane. Additionally, `.race` provides `padding: 14px 0 10px;`. Upward translation by `translateY(-4px)` during `.bob` now renders ear tips cleanly beyond the 44px SVG viewBox without horizontal clipping cutoff lines.
   - Pinned by `TestRaceHTML_SVGOverflowVisible` in `race_test.go`.

2. **M1 (Progress bar animated layout property `width`):**
   - Replaced layout-triggering `width: calc(var(--done) / var(--pages) * 100%)` and `transition: width 1.2s ease` on `.markers::before` with composite-only `transform: scaleX(calc(var(--done) / var(--pages)))`, `transform-origin: left`, and `transition: transform 1.2s ease`.
   - The fill transition now executes purely on the GPU compositor thread without triggering layout reflow or repainting during progress updates on phones or laptops.
   - Pinned by `TestRaceHTML_InterfaceIsOneNumber` and `TestRaceHTML_MotionAndReducedMotion` in `race_test.go`.

3. **M2 (Runner transition overshoot past container bounds at `--done: 8`):**
   - Replaced runner spring curve `cubic-bezier(.34, 1.2, .64, 1)` with monotonic deceleration curve `cubic-bezier(.25, 1, .5, 1)`.
   - Control points $y_1 = 1.0, y_2 = 1.0$ guarantee that curve progress $y(t) \le 1.0$ for all $t \in [0, 1]$, eliminating spring bounce overshoot.
   - At `--done: 8`, maximum runner translation right edge across all lanes is $100\text{cqw} - 8\text{px} + \text{jit} \le 100\text{cqw} - 2\text{px}$ (clearance $\ge 2\text{px}$ for Fox with `--jit: 6px`, up to 14px for Tortoise). Neither static nor dynamic translation ever crosses the container boundary.
   - Pinned by `TestRaceHTML_RunnerTransitionNoClipping` in `race_test.go`.

4. **L1 (Secondary SVG colors using hardcoded hex literals):**
   - Defined 24 semantic color tokens in `:root` and wrapped all 36 secondary SVG fills and strokes in `var(--token, #fallback)` tokens across all five animal sprites in `race.html` and `index.html`.
   - Enables complete palette and theme overrides by T9 without modifying `static/race/**`.
   - Pinned by `TestRaceHTML_AllSVGColorsUseVar` in `race_test.go`.

5. **L2 (`race_test.go` blind spots):**
   - Augmented `race_test.go` with `TestRaceHTML_SVGOverflowVisible`, `TestRaceHTML_AllSVGColorsUseVar`, `TestRaceHTML_RunnerTransitionNoClipping`, and expanded `TestRaceHTML_MotionAndReducedMotion` to scan all `transition:` declarations for forbidden layout properties (`width`, `height`, `top`, `left`, `bottom`, `right`, `margin`, `padding`).
   - Kept `race.html` and `index.html` verified byte-identical.

---

## Severity Counts

**0 C / 0 H / 0 M / 0 L**

---

## Remediation Evaluation Table

| # | Round 1 Finding | Severity | Evaluation / Audit Findings | Status |
|---|---|---|---|---|
| 1 | **H1** (`race.html:80, 95-98, 136-171, 211-246, 282-313`): SVG ears hard-clipped on `.bob` upward translation `translateY(-4px)` because HTML `<svg>` defaults to `overflow: hidden`. | **H** | **Resolved.** Added `.runner svg{overflow:visible}` in `race.html:123` and `index.html:123`. With 4px lane headroom (`.lane` 52px, `.runner` 44px at `top: 4px`) and 14px `.race` padding, ear tips (Hare y=1, Fox y=3, Mouse y=2.5) are fully preserved without flat-top truncation. Pin `TestRaceHTML_SVGOverflowVisible` verifies presence and fails if removed. | **CLOSED** (0 residue) |
| 2 | **M1** (`race.html:75`): `.markers::before` animated layout property `width` (`transition: width 1.2s ease`), violating PLAN.md §T9a composite-only transform requirement. | **M** | **Resolved.** Replaced `width` transition with `transform: scaleX(calc(var(--done) / var(--pages)))`, `transform-origin: left`, and `transition: transform 1.2s ease`. Zero layout reflow or repaint occurs during progress updates. Pin `TestRaceHTML_MotionAndReducedMotion` scans all transitions for layout properties and verifies composite-only scaleX. | **CLOSED** (0 residue) |
| 3 | **M2** (`race.html:85`): Runner transition `cubic-bezier(.34, 1.2, .64, 1)` overshot target translation, pushing runners at `--done: 8` up to 38px past container edge into `.race { overflow: hidden }`, chopping off heads and snouts. | **M** | **Resolved.** Replaced transition timing function with `cubic-bezier(.25, 1, .5, 1)`. Both $y_1$ and $y_2$ are $\le 1.0$, guaranteeing monotonic progress without overshoot. At `--done: 8`, all lanes maintain positive clearance ($2\text{px} \le \text{clearance} \le 14\text{px}$) from container width $100\text{cqw}$. Pin `TestRaceHTML_RunnerTransitionNoClipping` verifies $y_1, y_2 \le 1.0$ and checks non-negative clearance for all jitter values. | **CLOSED** (0 residue) |
| 4 | **L1** (`race.html:140+`): Over 30 secondary SVG fills and strokes used hardcoded hex literals instead of `var()` tokens, violating PLAN.md §T9a palette override rule. | **L** | **Resolved.** All 36 secondary SVG color attributes across Hare, Tortoise, Fox, Duck, and Mouse are wrapped in `var(--token, #fallback)` syntax, and 24 semantic tokens are declared in `:root`. Pin `TestRaceHTML_AllSVGColorsUseVar` verifies zero raw hex color attributes exist in SVGs. | **CLOSED** (0 residue) |
| 5 | **L2** (`race_test.go:239-266`): `TestRaceHTML_MotionAndReducedMotion` checked only `@keyframes` and missed `transition: width`, and lacked SVG overflow and runner clipping checks. | **L** | **Resolved.** `race_test.go` expanded to 472 lines with 8 comprehensive test functions covering SVG overflow, layout property scanning in transitions, cubic-bezier bounds, color tokenization, reduced-motion behavior, XML validity, and sprite uniqueness. | **CLOSED** (0 residue) |

---

## Mutation and Pin Robustness Analysis

Each pin was audited to verify that it is load-bearing and catches regressions if the remediation were reverted:

1. **H1 Pin (`TestRaceHTML_SVGOverflowVisible`):**
   - **Mutant:** Remove `.runner svg{overflow:visible}` from `race.html`.
   - **Pin behavior:** Regex fails with: `.runner svg must specify overflow: visible to prevent ear clipping during bob animation`.
   - **Assessment:** Directly tests the exact CSS rule required to prevent SVG clipping across all runners during `.bob`.

2. **M1 Pin (`TestRaceHTML_MotionAndReducedMotion` & `TestRaceHTML_InterfaceIsOneNumber`):**
   - **Mutant:** Revert `.markers::before` to `width: calc(var(--done) / var(--pages) * 100%)` and `transition: width 1.2s ease`.
   - **Pin behavior:** `TestRaceHTML_MotionAndReducedMotion` fails with: `transition animates forbidden layout property "width": width 1.2s ease`, and `TestRaceHTML_InterfaceIsOneNumber` fails on missing `scaleX(calc(var(--done) / var(--pages)))`.
   - **Assessment:** Catches any layout-triggering property in `transition:` or omission of composite-only `scaleX`.

3. **M2 Pin (`TestRaceHTML_RunnerTransitionNoClipping`):**
   - **Mutant:** Revert `.runner` transition to `cubic-bezier(.34, 1.2, .64, 1)`.
   - **Pin behavior:** Fails with: `runner cubic-bezier(.34, 1.2, .64, 1) has overshoot (y1=1.20, y2=1.00 > 1.0); must be <= 1.0 to prevent clipping at --done: 8`.
   - **Assessment:** Evaluates the cubic bezier mathematical control points $y_1$ and $y_2$ and verifies that static plus dynamic clearance $\ge 0$ across all lane jitters at `--done: 8`.

4. **L1 Pin (`TestRaceHTML_AllSVGColorsUseVar`):**
   - **Mutant:** Revert any SVG fill or stroke to raw hex literal (e.g. `fill="#d88c7d"`).
   - **Pin behavior:** Fails with: `SVG 1 contains raw hex color attribute "fill=\"#d88c7d\"" without var() wrapper (violation of PLAN.md §T9a)`.
   - **Assessment:** Exhaustively parses all SVG elements, rejects any `fill` or `stroke` containing a raw hex string not wrapped in `var()`, and requires all non-none color attributes to have a `var(--` prefix.

5. **L2 Pin (`race_test.go` suite):**
   - **Mutant:** Omit transition inspection from `TestRaceHTML_MotionAndReducedMotion`.
   - **Pin behavior:** Layout transitions would pass undetected. With the test in place, layout transitions fail immediately.

---

## Architectural & Style Compliance Check

- **Ownership Seam (AGENTS.md):**
  - Track T9a owns `static/race/**`.
  - Edits are strictly contained within `static/race/race.html`, `static/race/index.html`, and `static/race/race_test.go` (and review documentation under `dev-diary/adversarial-review/`).
  - Zero code modifications outside `static/race/**`.
- **Single-Property Interface (PLAN.md §T9a):**
  - The interface is solely `--done: 0..8` on `.race`.
  - Zero JavaScript logic in the component. Zero DOM manipulation. Server-renderable cold without JS.
  - The demo buttons at the bottom are explicitly marked as scaffolding that ships nowhere.
- **Animation and Performance (PLAN.md §T9a):**
  - Animations use `transform` only (`translateY`, `rotate`, `translateX`, `scaleX`).
  - No layout reflow or repaint triggered on phone or desktop.
- **`prefers-reduced-motion` Support:**
  - `@media (prefers-reduced-motion: reduce)` disables `.bob, .leg, .tail, .wing` animations and `.runner, .markers::before` transitions (`none !important`).
  - Progress markers and runner positions derive statically from `--done`, so progress information is fully preserved when motion is reduced.
- **Sprite Distinctiveness:**
  - All 5 animals (Hare, Tortoise, Fox, Duck, Mouse) have unique, identifiable silhouettes with distinct characteristics (ears, shells, scutes, snouts, tails, wings, bills, whiskers).
  - All 5 SVGs parse as valid XML and are verified pairwise unique by `TestRaceHTML_FiveDistinctAnimals`.
- **File Synchronization:**
  - `static/race/race.html` and `static/race/index.html` are byte-identical, verified by `TestRaceHTML_IdenticalCopies` and `diff -u`.

---

## Verification Gates Output

All verification gates were executed from the clean repository tree:

### 1. `go test ./static/race/... -v -race`
```console
=== RUN   TestRaceHTML_SelfContained
=== RUN   TestRaceHTML_SelfContained/race.html
=== RUN   TestRaceHTML_SelfContained/index.html
--- PASS: TestRaceHTML_SelfContained (0.00s)
    --- PASS: TestRaceHTML_SelfContained/race.html (0.00s)
    --- PASS: TestRaceHTML_SelfContained/index.html (0.00s)
=== RUN   TestRaceHTML_IdenticalCopies
--- PASS: TestRaceHTML_IdenticalCopies (0.00s)
=== RUN   TestRaceHTML_InterfaceIsOneNumber
--- PASS: TestRaceHTML_InterfaceIsOneNumber (0.00s)
=== RUN   TestRaceHTML_SVGOverflowVisible
--- PASS: TestRaceHTML_SVGOverflowVisible (0.00s)
=== RUN   TestRaceHTML_FiveDistinctAnimals
--- PASS: TestRaceHTML_FiveDistinctAnimals (0.01s)
=== RUN   TestRaceHTML_AllSVGColorsUseVar
--- PASS: TestRaceHTML_AllSVGColorsUseVar (0.02s)
=== RUN   TestRaceHTML_RunnerTransitionNoClipping
--- PASS: TestRaceHTML_RunnerTransitionNoClipping (0.00s)
=== RUN   TestRaceHTML_MotionAndReducedMotion
--- PASS: TestRaceHTML_MotionAndReducedMotion (0.00s)
PASS
ok  	thutapi/static/race	1.045s
```

### 2. `go test ./... -race`
```console
ok  	thutapi/cmd/thutapi	(cached)
ok  	thutapi/internal/audio	1.729s
ok  	thutapi/internal/bookvideo	(cached)
?   	thutapi/internal/gmi	[no test files]
ok  	thutapi/internal/gmi/media	(cached)
ok  	thutapi/internal/gmi/text	(cached)
ok  	thutapi/internal/illustrate	(cached)
ok  	thutapi/internal/interview	(cached)
ok  	thutapi/internal/job	(cached)
ok  	thutapi/internal/mediastore	(cached)
ok  	thutapi/internal/store	(cached)
ok  	thutapi/internal/story	(cached)
ok  	thutapi/internal/stream	(cached)
ok  	thutapi/static/race	(cached)
```

### 3. `go vet ./...`
```console
(clean, zero output)
```

### 4. `gofmt -l .`
```console
(clean, zero output)
```

### 5. `diff -u static/race/race.html static/race/index.html`
```console
(clean, byte-identical)
```

---

## Zero-Residue Claim

- **Critical (C):** 0 remaining (0 in round 1, 0 in round 2).
- **High (H):** 0 remaining (H1 fully remediated with `.runner svg{overflow:visible}` and verified unclipped).
- **Medium (M):** 0 remaining (M1 remediated with composite-only `transform: scaleX(...)`; M2 remediated with monotonic `cubic-bezier(.25, 1, .5, 1)` and positive clearance across all lanes).
- **Low (L):** 0 remaining (L1 remediated with complete `var(--token, fallback)` tokenization; L2 remediated with expanded test coverage).

There is **zero residue** against all findings from Round 1. No new defects or regressions were introduced.

---

## Final Verdict

**APPROVE (0/0/0/0)** — Track T9a satisfies all requirements in AGENTS.md and dev-diary/PLAN.md §T9a and is approved for landing on `main`.
