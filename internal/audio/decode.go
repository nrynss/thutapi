package audio

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MaxAudioBytes caps a single downloaded clip. One clip is ~63 KB
// (t2b-t5b-live-record.md: 63,348 bytes, a real 128 kbps MP3) and a
// whole book is eight of them; 16 MiB is far above any of them and
// well below anything that would threaten the box, so a malformed or
// hostile response cannot exhaust memory.
const MaxAudioBytes = 16 << 20

// defaultFetchTimeout bounds one audio download when Config.HTTPClient
// is nil. Generous for a storage.googleapis.com GET and short enough
// that a hung download cannot eat a page's whole budget.
const defaultFetchTimeout = 30 * time.Second

// maxRedirectHops bounds how many redirects fetchAudio follows. A
// storage.googleapis.com object may hop once to its serving edge;
// three is generous for that and short of anything a hostile response
// could use to walk the request around the local network.
const maxRedirectHops = 3

// queueRecord is the one envelope the request queue is known to
// answer with, read minimally: the terminal TTS record in
// t2b-t5b-live-record.md carries `payload` (the request echoed
// verbatim) beside `outcome` (the result, null until the job is
// terminal). Only the outcome subtree can carry the audio, so only
// that subtree is parsed; the payload echo is never consulted, and
// for a TTS record there is nothing in it this call did not already
// send.
type queueRecord struct {
	Outcome json.RawMessage `json:"outcome"`
}

// outcomeAudio is the outcome subtree's live-confirmed shape: the
// audio lives at outcome.audio_url, a public storage.googleapis.com
// object that expires (PLAN.md invariant 7), so it is downloaded on
// receipt. Nothing else in the outcome is read.
type outcomeAudio struct {
	AudioURL string `json:"audio_url"`
	Format   string `json:"format"`
}

// decodeAudioURL reads the audio out of a terminal TTS response: the
// outcome subtree's audio_url. The decode is keyed on the outcome
// alone — the payload beside it is the request echoed verbatim, and a
// walker that took "the first thing in the body that looks like
// media" is exactly how T6's round-1 H1 shipped. A body with no
// outcome.audio_url is a shape this package does not recognise and
// fails loudly: the media client returns every response raw and the
// terminal TTS record always carries the URL.
func decodeAudioURL(raw []byte) (string, error) {
	if len(raw) == 0 {
		return "", fmt.Errorf("%w: the response body was empty", ErrNoAudio)
	}
	var env queueRecord
	if err := json.Unmarshal(raw, &env); err != nil || !hasOutcome(env.Outcome) {
		return "", fmt.Errorf("%w: the response is not a request-queue audio envelope: %s", ErrNoAudio, excerpt(raw))
	}
	var out outcomeAudio
	if err := json.Unmarshal(env.Outcome, &out); err != nil {
		return "", fmt.Errorf("%w: the outcome subtree is not an audio record: %s", ErrNoAudio, excerpt(env.Outcome))
	}
	if !audioURL(out.AudioURL) {
		return "", fmt.Errorf("%w: outcome.audio_url is not an absolute http(s) URL: %q", ErrNoAudio, excerpt([]byte(out.AudioURL)))
	}
	return out.AudioURL, nil
}

// hasOutcome reports whether the envelope carried a result: an
// `outcome` key whose value is present and not null. `outcome: null`
// is a queued record — it has no result, and walking the rest of the
// body could only find the request's own echo.
func hasOutcome(raw json.RawMessage) bool {
	t := strings.TrimSpace(string(raw))
	return t != "" && t != "null"
}

// audioURL reports whether s is an absolute http or https URL. Other
// schemes are refused: anything else (file:, gs:) is not something
// this package will dereference.
func audioURL(s string) bool {
	if s == "" || len(s) > 2048 {
		return false
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// fetchAudio GETs one audio URL and returns its bytes and content
// type. It exists because a request-queue result names a
// storage.googleapis.com object rather than inlining it, and those
// objects are assumed to expire (PLAN.md invariant 7) — so the bytes
// are taken on receipt.
//
// This is not a call to GMI's inference API and does not cross PLAN.md
// invariant 1: the hosts that invariant names stay behind
// internal/gmi. Only http and https are followed, and the read is
// capped at MaxAudioBytes. The default client re-applies the scheme
// check on every hop of a redirect chain and caps the chain at
// maxRedirectHops: a 302 cannot steer the GET at another scheme, and
// a hostile chain cannot outgrow the cap. A same-scheme redirect to
// another host is still followed — the Location comes from the
// authenticated queue's own answer, and a storage edge hop is the
// legitimate case — but only for the bounded number of hops.
//
// The served Content-Type decides the stored type, not a body sniff:
// Go's content-type detection recognises an MP3 only by its ID3
// prefix, so an ID3-less MPEG stream — a perfectly real clip —
// sniffs as application/octet-stream. The storage edge's own header
// is the statement to trust (the live record verified "200
// audio/mpeg"); the sniff is only the fallback when no header is
// sent. The type must be audio/mpeg or audio/wav (audio/x-wav and
// audio/wave are accepted as wav spellings) — the closed set
// internal/mediastore can persist.
func (sp *speaker) fetchAudio(ctx context.Context, rawURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("audio: fetch audio %s: %w", rawURL, err)
	}
	resp, err := sp.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("audio: fetch audio %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("%w: fetch audio %s: HTTP %d", ErrNoAudio, rawURL, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxAudioBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("audio: fetch audio %s: %w", rawURL, err)
	}
	if len(b) > MaxAudioBytes {
		return nil, "", fmt.Errorf("%w: fetch audio %s: larger than %d bytes", ErrUnsupportedAudio, rawURL, MaxAudioBytes)
	}
	ct, ok, terr := audioType(resp.Header.Get("Content-Type"), b)
	if terr != nil {
		return nil, "", fmt.Errorf("audio: fetch audio %s: %w", rawURL, terr)
	}
	if !ok {
		return nil, "", fmt.Errorf("%w: fetch audio %s: answered with content type %q, want audio/mpeg or audio/wav", ErrNoAudio, rawURL, ct)
	}
	return b, ct, nil
}

// audioType resolves the content type of a downloaded clip. The
// response's Content-Type header governs (see fetchAudio for why a
// body sniff cannot); when the header is absent the sniff stands in.
// It returns the bare lower-cased type, ok=false when the bytes are
// not audio at all, and ErrUnsupportedAudio when they are audio in a
// type nothing downstream can store or play.
func audioType(header string, body []byte) (ct string, ok bool, err error) {
	ct = header
	if strings.TrimSpace(ct) == "" {
		ct = http.DetectContentType(body)
	}
	ct, _, _ = strings.Cut(ct, ";")
	ct = strings.ToLower(strings.TrimSpace(ct))
	switch ct {
	case "audio/mpeg":
		return ct, true, nil
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "audio/wav", true, nil
	case "":
		return ct, false, nil
	}
	if strings.HasPrefix(ct, "audio/") {
		return ct, false, fmt.Errorf("%w: %s (want audio/mpeg or audio/wav)", ErrUnsupportedAudio, ct)
	}
	return ct, false, nil
}

// safeRedirectPolicy is the default client's CheckRedirect: every hop
// must be http or https again, and the chain is capped. The default
// policy follows anything any reachable host answers with — a
// response-supplied URL would then be dereferenceable anywhere the box
// can reach, loopback and link-local included.
func safeRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirectHops {
		return fmt.Errorf("audio: stopped after %d redirects", maxRedirectHops)
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return fmt.Errorf("audio: redirect to %q refused: only http and https are followed", req.URL.Scheme)
	}
	return nil
}

// excerpt renders the head of a response body for an error message:
// enough to recognise what came back, never enough to paste a
// megabyte of base64 into a log line.
func excerpt(raw []byte) string {
	const max = 200
	s := strings.TrimSpace(string(raw))
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
