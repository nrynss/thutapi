# T2 round 1 — remediation

| | |
|---|---|
| **Target** | One M finding (M1) and one L finding (L1) in `dev-diary/adversarial-review/t2-round1.md`. Verdict was APPROVE w/ residue (0C/0H/1M/1L). |
| **Date** | 2026-09-04 |
| **Commit** | `4a176a2` — single focused commit covering the M1 fix and tests; L1 disposition is recorded here and the line is left as-is (see below). |
| **Round file** | `dev-diary/adversarial-review/t2-round1.md` is unchanged. |
| **Verification methodology** | (a) the Pin test passes on the fixed code; (b) applying the row's Mutation makes the Pin test fail; (c) reverting the Mutation restores the pass. Both directions run below. |

## Rows

### M1 — `classifyStatus` collapses every 4xx (that isn't 404) to `ErrTransient`

| Field | Value |
|---|---|
| **Severity** | M |
| **Where** | `internal/gmi/text/client.go` `classifyStatus` (lines ~217-234 pre-fix); same shape in `internal/gmi/media/client.go` `classifyStatus` (lines ~233-247 pre-fix). |
| **What** | Both clients had a four-case switch (401/403, 429, 404, default→`ErrTransient`). Any 4xx payload error (HTTP 400, 422) surfaced as `ErrTransient`. Downstream retry loops branch on `errors.Is(err, gmi.ErrTransient)` and would spin on a 400/422 that does not recover. The fix introduces a fifth typed sentinel, `ErrBadRequest`, and routes both 400 and 422 to it. The default branch is left covering other 4xx classes (3xx not followed, 418, etc.) as transient — they are not "fix the payload" and not "the server is broken", so the retry-with-caution stance is right. |
| **Pin** | `TestChat_BadRequest_400` in `internal/gmi/text/client_test.go` and `TestBadRequest_400` in `internal/gmi/media/client_test.go`. Each spins up a httptest server that returns HTTP 400 with a free-form error body, calls the client, asserts `errors.Is(err, gmi.ErrBadRequest) == true` AND `errors.Is(err, gmi.ErrTransient) == false`. |
| **Mutation** | In either client's `classifyStatus`, remove the new `case code == http.StatusBadRequest || code == http.StatusUnprocessableEntity:` branch so the default catches it. The Pin goes red: `errors.Is(err, gmi.ErrBadRequest) == false` (sentinel never matched) AND `errors.Is(err, gmi.ErrTransient) == true` (the regression class). |
| **Commit SHA** | `4a176a2` |
| **Verification (Pin on fixed)** | `go test ./internal/gmi/... -count=1 -race -v`: `TestChat_BadRequest_400 PASS`, `TestBadRequest_400 PASS`. Full suite: `go test ./... -count=1 -race -timeout 120s` → `ok thutapi/cmd/thutapi 9.897s`, `ok thutapi/internal/gmi/media 1.016s` (8 subtests, was 7), `ok thutapi/internal/gmi/text 1.015s` (6 subtests, was 5). gofmt -l . clean. |
| **Verification (Pin on Mutation)** | Restoring the pre-fix four-case switch (so the default case catches the 400) makes both Pins fail: the `errors.Is(err, gmi.ErrBadRequest) == true` assertion fails (sentinel never wrapped), and the negative `errors.Is(err, gmi.ErrTransient) == false` assertion fails (the regression class is back). |

### L1 — typo-key docstring: reviewer's line number is wrong, "in" is grammatical

| Field | Value |
|---|---|
| **Severity** | L |
| **Where** | `internal/gmi/media/client.go:158` — note: the reviewer cited line 157, but the line `spelled "volumn" in GMI's API` is line 158. |
| **What** | The reviewer's L1 finding states the docstring contains a stray "in" that should be removed. The actual line reads: `(note the missing 'u' — spelled "volumn" in GMI's API, match the typo or it is silently ignored)`. The word "in" before "GMI's API" is grammatical — the sentence reads "spelled 'volumn' [in] GMI's API", which is correct English. Removing "in" would produce "spelled 'volumn' GMI's API", which is ungrammatical. |
| **Pin** | `grep -n 'spelled "volumn" in' internal/gmi/media/client.go` — returns line 158. The Pin hits, but the reviewer's interpretation is the load-bearing question, not the grep. The "stray in" claim is wrong on inspection. |
| **Disposition** | L1 is recorded as a false-positive. The line is left as-is. A reviewer re-reading this row can verify by inspection: the line is natural English, not a typo. No code change. |
| **Why this isn't suppressed silently** | AGENTS.md says reviewers don't remediate their own findings; if I drop the row without saying so, the round file and remediation file disagree. Recording the disposition here means the audit trail is honest: one reviewer claim was investigated and rejected, with reasoning. |

## Aggregate verification (after the commit lands)

```
$ go vet ./...                                                  clean
$ go test ./... -count=1 -race -timeout 120s
ok   thutapi/cmd/thutapi           9.897s
ok   thutapi/internal/gmi/media    1.016s   (8 subtests)
ok   thutapi/internal/gmi/text     1.015s   (6 subtests)
$ gofmt -l .                                                     clean
$ bash -n deploy/docker-run.sh                                   clean
$ grep -c 'case code == http.StatusBadRequest' internal/gmi/text/client.go internal/gmi/media/client.go
internal/gmi/text/client.go:1
internal/gmi/media/client.go:1
```

M1 Pin tests run as part of the suite (TestChat_BadRequest_400 and TestBadRequest_400). L1's "in GMI's API" string remains in `internal/gmi/media/client.go:158` because the reviewer's interpretation does not survive inspection.

## Files changed in this commit

* `internal/gmi/errors.go` — adds `ErrBadRequest` typed sentinel; package doc example updated to show it.
* `internal/gmi/text/client.go` — `classifyStatus` routes 400/422 to `ErrBadRequest`.
*` `internal/gmi/text/client_test.go` — `TestChat_BadRequest_400` Pin.
* `internal/gmi/media/client.go` — `classifyStatus` routes 400/422 to `ErrBadRequest`.
* `internal/gmi/media/client_test.go` — `TestBadRequest_400` Pin.
* `dev-diary/PLAN.md` — T2 status row updated to DONE.

The two existing classifyStatus functions now have an explicit five-case shape:

```go
switch {
case code == http.StatusUnauthorized || code == http.StatusForbidden:
    return fmt.Errorf("%w: %s", gmi.ErrUnauthorized, msg)
case code == http.StatusTooManyRequests:
    return fmt.Errorf("%w: %s", gmi.ErrRateLimited, msg)
case code == http.StatusNotFound:
    return fmt.Errorf("%w: %s", gmi.ErrModelNotFound, msg)
case code == http.StatusBadRequest || code == http.StatusUnprocessableEntity:
    return fmt.Errorf("%w: %s", gmi.ErrBadRequest, msg)
case code >= 500:
    return fmt.Errorf("%w: %s", gmi.ErrTransient, msg)
default:
    return fmt.Errorf("%w: %s", gmi.ErrTransient, msg)
}
```

The default branch's docstring (in the text client only) explains why unmapped statuses are treated as transient: callers' retry loops have a chance to observe recovery, which is the right behaviour for the unknown-unknown class.