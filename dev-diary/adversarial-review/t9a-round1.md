# T9a round 1 — adversarial review of `static/race/**`

| | |
|---|---|
| **Target** | Track T9a, deliverables in `static/race/**`: `race.html`, `index.html`, `race_test.go`. |
| **Evidence** | `AGENTS.md` in full; `dev-diary/PLAN.md` §T9a; `dev-diary/prototypes/race.html`; `go test -v -race ./static/race` (PASS); `go vet ./...` (clean); `gofmt -l .` (clean); headless Firefox rendering and DOM measurements at 900px (desktop) and 390px (mobile); pixel-level visual inspections across `--done` states 0, 3, and 8. |
| **Method** | Adversarial audit against AGENTS.md, PLAN.md §T9a ("The race", "Done when"). Verified seam boundaries (no files outside `static/race/**`). Tested single-property interface purity (`--done` on `.race`). Inspected sprite distinctiveness and SVG silhouettes for all 5 animals. Verified animation constraints (composite-only `transform` vs layout properties in `@keyframes` and `transition`). Verified `prefers-reduced-motion` compliance. Analyzed runner geometry and container bounds across responsive viewports. |
| **Date** | 2026-09-05. Reviewer: fresh adversarial review agent. No remediation attempted; review only. |

**Verdict: REMEDIATE — 0 × C, 1 × H, 2 × M, 2 × L.**

---

## Summary of the diff

Track T9a delivers the self-contained "waiting race" screen (screen 5 of PLAN.md §The flow) and animal sprites:

1. **`static/race/race.html` & `static/race/index.html`**:
   - Single custom property interface: `--done: 0..8` on `.race`. Zero hand-rolled DOM manipulation or JS rendering; works cold without JS.
   - Demo buttons at bottom explicitly labelled as scaffolding that ships nowhere.
   - Five custom SVG animal sprites: Hare/Rabbit (upright ears, haunch, fluffy tail), Tortoise (domed shell, scutes, rim, neck, stubby legs), Fox (sharp erect black-tipped ears, tapered muzzle, bushy white-tipped tail, stockings), Duck (bill, fluttering wing, upturned tail, webbed feet), and Little Mouse (giant saucer ears, whiskers, teardrop body, sinuous whip tail).
   - Container queries (`cqw`) resolving against `.race` for responsive phone (390px) and laptop (900px+) rendering.
   - CSS `@keyframes` animations for stride, limb bob, tail wag, and wing flap, with full `prefers-reduced-motion: reduce` support.
2. **`static/race/race_test.go`**:
   - Tests ensuring files are self-contained (no CDN/remote dependencies, inline styles & SVGs only).
   - Verifies `race.html` and `index.html` stay byte-identical.
   - Validates the `--done` single-number interface and 8 marker cells.
   - XML parses and asserts presence and distinctness of all 5 SVG sprites.
   - Asserts `@keyframes` do not animate layout properties and verifies reduced-motion disabling.

The sprites are charming, expressive, and distinct, and the single-property architecture cleanly isolates the component before T9 Preact integration. However, five adversarial findings were uncovered: ear clipping during body bob, layout property animation on the progress fill, transition overshoot collision at `--done: 8`, un-tokenized secondary colors, and test suite blind spots.

---

## Severity counts

**0 C / 1 H / 2 M / 2 L**

---

## Findings table

| # | Sev | Where | What | Pin | Mutation |
|---|---|---|---|---|---|
| 1 | **H** | `static/race/race.html:80, 95-98, 136-171, 211-246, 282-313` | SVG ears hard-clipped on `.bob` animation. The `.bob` keyframe translates bodies upward by `translateY(-4px)`, pushing ear tips above y=0 (Hare y=-3, Fox y=-1, Mouse y=-1.5). Because HTML `<svg>` defaults to `overflow: hidden`, ear silhouettes are chopped flat across the top during the continuous running animation. | Headless render probe of Hare/Fox/Mouse at `translateY(-4px)` reveals horizontal truncation line; assert `.runner svg` specifies `overflow: visible` (or viewBox provides 4px top headroom) | Removing `overflow: visible` from `.runner svg` re-introduces the flat-top ear truncation |
| 2 | **M** | `static/race/race.html:75` | `.markers::before` animates layout property `width` (`transition: width 1.2s ease`). Violates PLAN.md §T9a: *"Animate transform only. Nothing that triggers layout, on a screen that holds for six minutes on a phone."* | Regex asserting that no `transition:` rule animates `width`, `height`, `top`, `left`, `margin`, or `padding` fails on `transition: width 1.2s ease` | Changing composite-only `transform: scaleX(...)` with `transform-origin: left` back to `transition: width` triggers the pin |
| 3 | **M** | `static/race/race.html:85` | Runner transition uses `cubic-bezier(.34, 1.2, .64, 1)` with `y1 = 1.2` overshoot. At `--done: 8`, runners sit within 2px–14px of the right track edge. During transition, the 5–8% spring overshoot pushes runners up to 38px past the container edge (`rect.right: 918px > race.right: 880px`), causing heads and snouts to be sliced off by `.race { overflow: hidden }` before bouncing back. | Headless render probe measuring `max(runner.right) <= race.right` during `--done: 8` transition fails (`918px > 880px`) | Re-introducing `cubic-bezier(.34, 1.2, .64, 1)` without sufficient right overshoot padding fails the bound check |
| 4 | **L** | `static/race/race.html:140, 178, 253, 289` | Secondary SVG fills and strokes use hardcoded hex literals (e.g. `#d88c7d`, `#6c8c56`, `#faedd9`, `#e0a028`, `#d4887b`) instead of `var()` tokens, violating PLAN.md §T9a: *"Every colour is a var() so T9's palette drops in without touching this track."* | Regex searching for raw hex color attributes `(fill\|stroke)="#..."` not wrapped in `var(...)` returns 30+ instances | Replacing `var(--hare-ear-inner, #d88c7d)` with `#d88c7d` triggers the check |
| 5 | **L** | `static/race/race_test.go:239-266` | `TestRaceHTML_MotionAndReducedMotion` checks only `@keyframes` blocks and ignores `transition:`, leaving the test blind to `transition: width`. It also lacks any assertion verifying that SVG geometry remains unclipped during animations. | Mutating `race.html` to add `transition: width` still passes `race_test.go` | Removing `transition:` parsing from the test suite allows layout transitions to pass undetected |

---

## Detailed findings

### H1 — SVG ear tips hard-clipped during `.bob` animation

**Severity:** H. (Real visual defect on the primary deliverable; the silhouette distinctiveness of the animals' ears is truncated on every stride throughout the 6-minute wait.)

**Where:**
- `static/race/race.html:80`: `.runner{position:absolute;top:4px;left:0;width:var(--runner-w);height:44px;...}`
- `static/race/race.html:95-98`: `@keyframes bob{from{transform:translateY(0) rotate(-1.2deg)} to{transform:translateY(-4px) rotate(1.2deg)}}`
- `static/race/race.html:169-170`: Hare front ear `M48 18 C47 9, 50 1, 54 1 C57 1, 56 8, 52 18 Z` (starts at `y=1`)
- `static/race/race.html:243-245`: Fox ear `polygon points="48,14 52,3 56,14"` (starts at `y=3`)
- `static/race/race.html:311-312`: Mouse front ear `circle cx="48" cy="9" r="6.5"` (reaches `y = 9 - 6.5 = 2.5`)

**What:**
The SVGs use `viewBox="0 0 64 44"` with height 44px. The Hare's tall upright ears reach `y=1`, the Fox's black-tipped ears reach `y=3`, and the Mouse's saucer ear reaches `y=2.5`.
The `.bob` animation translates `<g class="bob">` upward by `translateY(-4px)`.
At the top of the bob:
- Hare ear tip moves to `y = 1 - 4 = -3px`.
- Fox ear tip moves to `y = 3 - 4 = -1px`.
- Mouse saucer ear moves to `y = 2.5 - 4 = -1.5px`.

By W3C SVG specification and user agent stylesheets, HTML `<svg>` elements default to `overflow: hidden`. Any geometry rendered outside the `0 0 64 44` viewBox is clipped.
Consequently, on every single stride, the tips of the Hare's ears, Fox's ears, and Mouse's ears are sliced flat by a harsh horizontal cutoff line at `y=0`.
Notably, `.lane` has `height: 52px` while `.runner` is `height: 44px; top: 4px;`, meaning `.lane` already provides 4px of headroom above the runner. Setting `overflow: visible` on `.runner svg` (or increasing viewBox headroom) allows the ears to render completely without truncation.

**Pin:** Headless browser screenshot of Hare/Fox/Mouse at `translateY(-4px)` demonstrates flat-top ear truncation. Assert in test that `.runner svg` has `overflow: visible` (or equivalent).

**Mutation:** Removing `overflow: visible` reintroduces the ear clipping.

---

### M1 — `.markers::before` animates layout property `width` instead of composite-only `transform`

**Severity:** M. (Real defect with workaround; triggers layout and recalculation during progress bar updates on a screen that must run smoothly for six minutes on mobile.)

**Where:**
- `static/race/race.html:73-75`
```css
  .markers::before{content:"";position:absolute;inset:0;
    width:calc(var(--done) / var(--pages) * 100%);
    background:rgba(201,136,90,.16);transition:width 1.2s ease}
```

**What:**
PLAN.md §T9a explicitly states:
> *"Animate `transform` only. Nothing that triggers layout, on a screen that holds for six minutes on a phone."*

Animating `width` forces the browser to recalculate element geometry, perform layout reflow, and repaint pixels during the 1.2s transition each time `--done` increments.
This can be implemented entirely on the GPU compositor without triggering layout by setting:
```css
  .markers::before{content:"";position:absolute;inset:0;
    transform-origin:left;
    transform:scaleX(calc(var(--done) / var(--pages)));
    background:rgba(201,136,90,.16);transition:transform 1.2s ease}
```
Under this form, the progress bar transition animates `transform: scaleX(...)`, maintaining 100% composite-only performance.

**Pin:** Automated regex probe scanning all `transition:` declarations in the stylesheet for layout properties (`width:`, `height:`, `top:`, `left:`, `margin:`, `padding:`). Currently flags line 75 `transition:width 1.2s ease`.

**Mutation:** Changing `transform: scaleX(...)` back to `transition: width 1.2s ease` trips the pin.

---

### M2 — Runner transition overshoots and clips at `--done: 8`

**Severity:** M. (Real defect; causes runners to slam into the right border and have their heads chopped off by `overflow: hidden` when reaching the finish line.)

**Where:**
- `static/race/race.html:80-85`
```css
  .runner{position:absolute;top:4px;left:0;width:var(--runner-w);height:44px;
    transform:translateX(calc(
      var(--track-pad,8px) +
      (var(--done) / var(--pages)) * (100cqw - var(--runner-w) - (2 * var(--track-pad,8px))) +
      var(--jit,0px)));
    transition:transform 1.4s cubic-bezier(.34,1.2,.64,1)}
```

**What:**
The transition uses `cubic-bezier(.34, 1.2, .64, 1)`. The `1.2` parameter specifies a spring bounce curve whose peak exceeds 1.0 (overshooting target translation by ~5–8% of travel distance).
At `--done: 8`, the runners statically finish near the right edge:
- For Fox (`--jit: 6px`), static right edge is `880px - 2px = 878px` on a 900px screen (only 2px from the container wall).
- For Hare (`--jit: 4px`), static right edge is `876px` (4px from the container wall).

When transitioning to `--done: 8`, the spring overshoot pushes Fox to `translateX(834px)`, placing its right bounding edge at `918px` (`854px + 64px = 918px`).
Because `.race` has `width: 860px`, `right: 880px`, and `overflow: hidden`, the runners overshoot `38px` past the track boundary. Their heads, snouts, and front paws are clipped off-screen by `.race`'s right border before springing backward.
On cold-loading `--done: 8`, initial transition timing in browsers captures the animals sliced in half.
The spring effect should be bounded so that `translateX + var(--runner-w) <= 100cqw` throughout the entire transition curve, or right padding/margin provided to absorb the overshoot.

**Pin:** Headless browser test advancing `--done` from 7 to 8 (or 0 to 8) asserting `max(runner.getBoundingClientRect().right) <= race.getBoundingClientRect().right`. Fails with `runner.right = 918px` vs `race.right = 880px`.

**Mutation:** Restoring `cubic-bezier(.34, 1.2, .64, 1)` without overshoot headroom trips the boundary check.

---

### L1 — Secondary SVG colors use raw hex literals instead of `var()` tokens

**Severity:** L. (Polish/theming hygiene; prevents full palette override by T9.)

**Where:**
- `static/race/race.html:140, 147, 148, 153, 164, 165, 167` (Hare inner ear `#d88c7d`, tail `#fff9f0`, belly `#faedd9`, nose `#d9777f`)
- `static/race/race.html:178, 181, 187, 192-196, 199, 204, 205` (Tortoise tail/legs `#6c8c56`, `#7a9c64`, scutes `#6f915b`, smile `#3a4e28`, rim `#8da860`)
- `static/race/race.html:215, 219, 229, 237, 239-241, 245` (Fox inner ear `#faedd9`, tail tip/bib `#fff9f2`, nose `#1a100c`)
- `static/race/race.html:253, 256, 260, 267, 268, 272-274, 277, 278` (Duck tail/belly/wing `#e0a028`, feet `#c45014`, bill shade `#b84c12`, nostril `#8a3408`)
- `static/race/race.html:286, 287, 289, 292, 297, 304, 306-309` (Mouse back ear `#7a6557`, `#c47d72`, tail `#d4887b`, belly `#f5eae1`, nose `#d9777f`, whiskers `#4a3b32`)

**What:**
PLAN.md §T9a states:
> *"Every colour is a `var()` so T9's palette drops in without touching this track."*

While primary coats declare tokens like `var(--hare, #c9885a)`, over 30 secondary fills and strokes are hardcoded hex values without `var()` wrappers. Wrapping them in semantic or fallback tokens (e.g. `var(--hare-ear-inner, #d88c7d)`, `var(--tail-white, #fff9f0)`) allows T9 to customize or invert the palette cleanly.

**Pin:** Script regex scanning SVG elements for `(fill|stroke)="(#[0-9a-fA-F]{3,8})"` not wrapped in `var(...)`.

**Mutation:** Replacing `var(--hare-ear-inner, #d88c7d)` with `#d88c7d` fails the pin.

---

### L2 — `race_test.go` animation verification omits `transition:` and SVG clipping

**Severity:** L. (Test hygiene and verification gap.)

**Where:**
- `static/race/race_test.go:239-266` (`TestRaceHTML_MotionAndReducedMotion`)

**What:**
`TestRaceHTML_MotionAndReducedMotion` parses `@keyframes` blocks to ensure layout properties (`width:`, `height:`, etc.) are not animated, but completely omits checking `transition:` properties. As a result, `.markers::before { transition: width 1.2s ease }` passed undetected.
Additionally, the test suite verifies that SVG XML parses and silhouettes are unique, but does not assert that coordinates stay within the visible viewBox during animated transforms (`.bob`, `stride`).

**Pin:** Add `transition:` layout property checks and SVG clipping assertions to `race_test.go`; currently fails on `transition: width`.

**Mutation:** Removing `transition:` inspection from `TestRaceHTML_MotionAndReducedMotion` allows layout-triggering transitions to pass.
