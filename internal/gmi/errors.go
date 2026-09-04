// Package gmi wraps the two GMI Cloud inference shapes used by Thutapi:
// the OpenAI-compatible chat completions endpoint for text, and the
// {model, payload} request-queue endpoint for images and audio.
//
// Transport errors are surfaced as typed sentinels so callers can branch
// with errors.Is without parsing free-form provider error strings:
//
//	var err error
//	resp, err = client.Chat(ctx, req)
//	if errors.Is(err, gmi.ErrBadRequest)    { ... fix payload, do not retry ... }
//	if errors.Is(err, gmi.ErrRateLimited)   { ... retry with backoff ... }
//	if errors.Is(err, gmi.ErrUnauthorized)  { ... refresh key, surface ... }
//	if errors.Is(err, gmi.ErrModelNotFound) { ... the model id is wrong ... }
//	if errors.Is(err, gmi.ErrPaymentRequired) { ... billing — operator action ... }
//	if errors.Is(err, gmi.ErrTransient)     { ... already retried once; caller MAY retry again ... }
package gmi

import "errors"

// ErrBadRequest is returned on any 4xx that is the request payload's
// fault and has no sentinel of its own: 400, 413 (payload too large),
// 422, 418, and so on. 402 has its own sentinel (billing is operator
// action, not a payload fix). Fix the payload; do not retry — the
// same bytes will fail the same way.

var ErrBadRequest = errors.New("gmi: bad request")

// ErrPaymentRequired is returned on HTTP 402. The free-tier window
// has closed or the account cannot be billed — a state only the
// operator can fix (PLAN.md §T11.2 treats billing as its own risk).
// Never retried, internally or by classification; surface to the
// operator.
var ErrPaymentRequired = errors.New("gmi: payment required")

// ErrTransient is returned on HTTP 5xx, on transport-layer failures
// (network reset, deadline exceeded), on a request-queue response
// whose status is "failed" once the retry budget is spent, and on a
// 2xx body that fails to decode. Each client retries such a failure
// exactly once internally, per PLAN.md §T2; beyond that one retry,
// caller-level retry with a fresh context is the expected pattern.
var ErrTransient = errors.New("gmi: transient error")

// ErrUnauthorized is returned when GMI rejects the request with HTTP 401
// or 403. The most likely cause is a missing or expired GMI_API_KEY — the
// container env was not set or the key was rotated. Surface to the
// operator; do not retry blindly.
var ErrUnauthorized = errors.New("gmi: unauthorized")

// ErrRateLimited is returned on HTTP 429. GMI caps per-minute request
// throughput; back off and retry with the same request, do not change
// anything.
var ErrRateLimited = errors.New("gmi: rate limited")

// ErrModelNotFound is returned on HTTP 404 from either endpoint — text
// and request queue alike. Classification is status-only: any 404 maps
// to this sentinel regardless of what the error message says, and the
// message is never inspected (AGENTS.md §Errors forbids matching a
// substring of a provider's error message — the upstream text is
// surfaced verbatim for the operator). The text endpoint surfaces the
// sentinel when the caller forgets the "MiniMaxAI/" prefix on the
// model id — bare "MiniMax-M3" 404s against api.gmi-serving.com. Fix
// the model id, do not retry.
var ErrModelNotFound = errors.New("gmi: model not found")
