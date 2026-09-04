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
//	if errors.Is(err, gmi.ErrTransient)     { ... transport blip, retry ... }
package gmi

import "errors"

// ErrBadRequest is returned on HTTP 400 and 422 against either endpoint.
// The caller payload was malformed (unsupported field, missing required
// field, image too large, model id wrong format even when the model
// exists, etc). Fix the payload; do not retry — the same bytes will fail
// the same way.
var ErrBadRequest = errors.New("gmi: bad request")

// ErrUnauthorized is returned when GMI rejects the request with HTTP 401
// or 403. The most likely cause is a missing or expired GMI_API_KEY — the
// container env was not set or the key was rotated. Surface to the
// operator; do not retry blindly.
var ErrUnauthorized = errors.New("gmi: unauthorized")

// ErrRateLimited is returned on HTTP 429. GMI caps per-minute request
// throughput; back off and retry with the same request, do not change
// anything.
var ErrRateLimited = errors.New("gmi: rate limited")

// ErrModelNotFound is returned on HTTP 404 against the text endpoint,
// and on any request-queue response whose error message identifies a
// missing or unknown model. The text endpoint surfaces this when the
// caller forgets the "MiniMaxAI/" prefix on the model id — bare
// "MiniMax-M3" 404s against api.gmi-serving.com. Fix the model id, do
// not retry.
var ErrModelNotFound = errors.New("gmi: model not found")

// ErrTransient is returned on HTTP 5xx and on transport-layer failures
// (network reset, deadline exceeded against a still-open body, request
// queue returning {"status":"failed"} once retry budget is spent).
// The caller MAY retry, but each client only retries once internally —
// caller-level retry with a fresh context is the expected pattern.
var ErrTransient = errors.New("gmi: transient error")
