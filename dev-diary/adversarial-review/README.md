# Adversarial review

One file per review round. A track is not done because it runs; it is done when
a round says so.

**Loop:** implement → `<track>-round<K>.md` → remediate via
`<track>-remediation-round<K>.md` → `<track>-round<K+1>.md` → … →
**APPROVE, zero residue**.

**Naming:** `t6-round1.md`, `t6-remediation-round1.md`, `t6-round2.md`,
`t6-remediation-round2.md`, … Tracks are numbered `t0`–`t14`.

**Roles:** implementation agent implements. Reviewer reviews (verdict
REMEDIATE or APPROVE). Remediation agent fixes the reviewer's findings.
**A reviewer never remediates its own finding in the same round.** A separate
review round follows every remediation.

**Per-finding schema** (mandatory for every finding):

| Column | Meaning |
|---|---|
| **Severity** | One of C, H, M, L (see key below). |
| **Where** | File path and line number. |
| **What** | Observable defect or claim. |
| **Pin** | Failing test name, shell probe, or reviewer-written snippet that fails on the bug. |
| **Mutation** | The one- or two-line edit that re-introduces the bug — proves the Pin is load-bearing. |

A finding without a Pin is not actionable. A finding without a Mutation is not
load-bearing.

## Severity key

| Level | Meaning |
|---|---|
| **C** Critical | Breaks the demo. Cannot ship. |
| **H** High | Real defect the demo survives. Must fix before the track closes. |
| **M** Medium | Real defect with a workaround. Must fix before the track closes. |
| **L** Low | Polish / hygiene. Must fix before the track closes. |

**No severity is exempt.** Every finding lands in a remediation file. The only exception: a finding so trivial that it is a single-line doc typo or a single type annotation that needs no review — the implementer may fix it in the same commit that produced the finding, and the review file must record the in-line fix by name and file. Anything beyond that goes through the normal remediation round.

**APPROVE** verdict requires an explicit "zero residue" claim against every
prior round's findings, severity by severity.

## Workflow binding

This README is the loop definition that AGENTS.md references. Any change to
the loop (severity vocabulary, file naming, exemption rules) updates BOTH
files in the same commit.