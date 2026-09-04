# Adversarial review

One file per review round. A track is not done because it runs; it is done when
a round says so.

**Loop:** implement → `<track>-round<K>.md` → remediate via
`<track>-remediation-round<K>.md` → `<track>-round<K+1>.md` → … →
**APPROVE, zero residue**.

**Naming:** `t6-round1.md`, `t6-remediation-round1.md`, `t6-round2.md`,
`t6-remediation-round2.md`, … Tracks are numbered `t0`–`t14`.

**Roles — four, and they stay separate.** Full definition in `AGENTS.md`
§Agentic development; the short form:

| Role | Does | Never does |
| --- | --- | --- |
| **Orchestrator** | Dispatches the other three, gates the loop, lands the commit, fixes trivially-exempt L's itself | Implements; writes a verdict |
| **Implementation agent** | Implements inside the track's `Owns` paths | Reviews its own work |
| **Reviewer** | Writes `t<N>-round<K>.md` — verdict, severity counts, one row per finding | Fixes what it found |
| **Remediation agent** | Fixes every finding; writes `t<N>-remediation-round<K>.md` | Changes the verdict; fixes things nobody found |

**A reviewer never remediates its own finding in the same round**, and a
separate review round follows every remediation. Use a **fresh agent per role
per round** — a reviewer who has seen the implementer's reasoning is not
adversarial, and a second round run by the first round's reviewer inherits its
blind spots (see `t2-round3.md` §"Why the earlier rounds missed this").

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

*(P0–P3 vocabulary maps as P0 = C, P1 = H, P2 = M, P3 = L. Write C/H/M/L in
review files so counts stay comparable across rounds.)*

**No severity is exempt.** Every finding lands in a remediation file — L and
P3 included. "It's only an L" is not a disposition; recording a finding as a
**false positive** is (round 1's L1 was, correctly), but that is a judgement
about whether the defect is real, never about whether a real defect is worth
fixing.

**The one exemption — the orchestrator's fast path.** An **L** that is a doc
typo, a comment fix or a single type annotation **that cannot change
behaviour** may be fixed directly in the same commit, without a remediation
agent and without another review round; the orchestrator then closes the track.
All four conditions must hold: (1) the finding is L, never M or above;
(2) the fix cannot alter behaviour — prose only, no value, branch or signature;
(3) the review file records the in-line fix by name and file; (4) if it is
arguable, it is not trivial. Anything else goes through the normal remediation
round.

**A comment that misdescribes behaviour is not a doc defect.** It carries the
severity of the behaviour it misdescribes, because the next agent codes against
it. A docstring claiming a retry the code does not perform is an H
(`t2-round3.md` H2), not an exempt typo.

**APPROVE** verdict requires an explicit "zero residue" claim against every
prior round's findings, severity by severity.

## Workflow binding

This README is the loop definition that AGENTS.md references. Any change to
the loop (severity vocabulary, file naming, exemption rules) updates BOTH
files in the same commit.