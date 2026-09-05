package illustrate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// MaxImageBytes caps a single decoded or fetched image. Ten images a
// book at a few hundred kilobytes each is the working size; 16 MiB is
// far above any of them and well below anything that would threaten
// the box, so a malformed or hostile response cannot exhaust memory.
const MaxImageBytes = 16 << 20

// defaultFetchTimeout bounds one image fetch when Config.HTTPClient
// is nil. Generous for a storage.googleapis.com GET and short enough
// that a hung download cannot eat a page render's whole budget.
const defaultFetchTimeout = 30 * time.Second

// minBase64Len is the shortest string the walk will try to base64-
// decode as an image. Request ids and status words are far shorter,
// so this skips them without a special case; anything that survives
// still has to decode to real image magic before it is accepted.
const minBase64Len = 64

// supportedTypes is the closed set of image types this package hands
// back. It is deliberately the same set internal/mediastore accepts
// for image blobs: an image that cannot be persisted is not a
// successful render, and finding that out here — with the model id
// and the prompt still in hand — is far more useful than finding it
// out at the store.
var supportedTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
}

// decodeImage turns one request-queue response body into image bytes,
// their content type, and the URL the bytes came from ("" when the
// body carried the picture itself).
//
// internal/gmi/media hands every response back as raw bytes on
// purpose: the request queue returns a different result schema per
// model and GMI has published none of them, so a typed struct there
// would be a fabrication (PLAN.md §T2, amended at t2-round3.md M3).
// The decode therefore belongs here, in the track that chose the
// model — but this package cannot honestly hard-code an unpublished
// schema either. What it can do is key on the one shape the queue is
// live-confirmed to produce (the polled image records in
// t6b-live-record.md §3, identical whether the job finished on the
// POST or was polled to terminal) and stay structural outside it:
//
//  1. A body that is already image bytes is passed through.
//  2. A queue envelope — a JSON object with an outcome subtree —
//     decodes from that subtree alone, by name: outcome.media_urls,
//     a LIST OF OBJECTS {"id","url"}, and the first entry's .url is
//     the picture (one image per request; fetched, because such
//     links expire — PLAN.md invariant 7). The thumbnail beside it
//     (outcome.thumbnail_image_url) is never the picture this call
//     asked for and is never consulted. The payload beside the
//     outcome is the request echoed verbatim — the t2i record echoes
//     the prompt, the i2i record echoes {} — and it is never
//     parsed. A queued record's `outcome: null` is a body with no
//     picture in it, not an invitation to go looking. An outcome
//     with no media_urls fails loudly: the terminal image records
//     always carry at least one entry, so an envelope without one is
//     a shape this package does not recognise, and guessing could
//     only pick the request's own echo.
//  3. A body with no outcome subtree — any schema GMI never
//     published — is walked: every string, in sorted-key order, and
//     the first that is a data: URI or base64 that decodes to real
//     image magic is the image; failing that, the first http/https
//     URL is fetched. Magic is the test, not the field name, so a
//     schema this package has never seen still works and a request
//     id can never be mistaken for a picture. Candidates equal to
//     what this call itself sent (sent) — the reference sheet bytes,
//     or a reference sheet URL echoed back inside payload.image —
//     are the request's own echo, not a result, and are stepped
//     over.
//
// Errors: a body with no image is ErrNoImage. In the walk, an
// unsupported-type candidate or a URL that fails is remembered and
// surfaces only if nothing usable is found anywhere in the body —
// one bad candidate must not fail a render that carries a good
// image, and a single-candidate body still reports it, deferred
// rather than swallowed. In the outcome subtree the answer is where
// it has to be, so an unusable answer fails immediately. Neither
// error is ever silently swallowed: a page with no picture must fail
// loudly, since the alternative is a book with a hole in it.
func (r *renderer) decodeImage(ctx context.Context, raw []byte, sent sentImages) ([]byte, string, string, error) {
	if len(raw) == 0 {
		return nil, "", "", fmt.Errorf("%w: the response body was empty", ErrNoImage)
	}
	if ct, ok, err := imageType(raw); err != nil {
		return nil, "", "", err
	} else if ok {
		return raw, ct, "", nil
	}

	// A queue envelope keys the decode on the outcome subtree: that
	// is where the result lives, and the payload beside it is the
	// request echoed verbatim (live-confirmed; see queueRecord).
	var env queueRecord
	if json.Unmarshal(raw, &env) == nil && hasOutcome(env.Outcome) {
		return r.decodeOutcome(ctx, env.Outcome)
	}

	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, "", "", fmt.Errorf("%w: the response is neither image bytes nor JSON: %s", ErrNoImage, excerpt(raw))
	}
	strs := collectStrings(doc, nil)

	// The walk is structural and defensive: a candidate that decodes
	// but sniffs as an unsupported type — or a URL that fails — is
	// remembered, not fatal, because the body may carry a usable
	// image further along and a failed page fails the whole book.
	var firstErr error
	for _, s := range strs {
		b, ok := decodeBase64Image(s)
		if !ok {
			continue
		}
		if sent.hasBytes(b) {
			continue
		}
		ct, isImage, err := imageType(b)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if isImage {
			return b, ct, "", nil
		}
	}
	for _, s := range strs {
		u, ok := imageURL(s)
		if !ok {
			continue
		}
		if sent.hasURL(u) {
			continue
		}
		b, err := r.fetchImage(ctx, u)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ct, isImage, err := imageType(b)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !isImage {
			if firstErr == nil {
				firstErr = fmt.Errorf("%w: %s answered with %d bytes that are not an image", ErrNoImage, u, len(b))
			}
			continue
		}
		return b, ct, u, nil
	}
	if firstErr != nil {
		return nil, "", "", firstErr
	}
	return nil, "", "", fmt.Errorf("%w: no image, data: URI or image URL in the response: %s", ErrNoImage, excerpt(raw))
}

// queueRecord is the one envelope the request queue is known to
// answer with, read minimally: the terminal records in
// t6b-live-record.md §3 — identical for a job that finished on the
// POST and one that was polled to terminal — carry `payload` (the
// request echoed verbatim) beside `outcome` (the result, null until
// the job is terminal). Only the outcome subtree can carry the
// picture, so only that subtree is parsed; the payload echo — the
// prompt on a t2i record, `{}` on the live i2i record — is never
// consulted, and for an EditImage record there is nothing in it this
// call did not already send.
type queueRecord struct {
	Outcome json.RawMessage `json:"outcome"`
}

// mediaURL is one entry of outcome.media_urls, per the terminal image
// records in t6b-live-record.md §3: an object with an id and a URL.
// The URL is what is fetched and chained.
type mediaURL struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// outcomeMedia is the result subtree's live-confirmed shape. Only
// media_urls is read: the thumbnail sitting beside it
// (outcome.thumbnail_image_url) is a smaller preview, not the picture
// this call asked for, and a walker that took "the first URL under
// outcome" could pick it — which is precisely why the decode is keyed
// by name. The older TTS-side field names (image, image_url) are not
// part of the image records' shape and are not consulted either.
type outcomeMedia struct {
	MediaURLs []mediaURL `json:"media_urls"`
}

// decodeOutcome reads the picture out of an envelope's outcome
// subtree: the first media_urls entry's .url, fetched because such
// links expire (PLAN.md invariant 7). The subtree is the model's
// answer and the only place a result can be, so a candidate here is
// not deferred the way the walk's are: a GIF at media_urls[0] is the
// answer itself being unusable, and fails loudly. One entry is taken
// because one image is requested (media.DefaultMaxImages); were a
// sequence ever returned, the first frame is the deterministic
// answer.
func (r *renderer) decodeOutcome(ctx context.Context, outcome json.RawMessage) ([]byte, string, string, error) {
	var out outcomeMedia
	if err := json.Unmarshal(outcome, &out); err != nil {
		return nil, "", "", fmt.Errorf("%w: the outcome subtree is not a media_urls record: %s", ErrNoImage, excerpt(outcome))
	}
	if len(out.MediaURLs) == 0 {
		return nil, "", "", fmt.Errorf("%w: the outcome subtree carries no media_urls — the terminal image records always carry at least one (t6b-live-record.md §3)", ErrNoImage)
	}
	u := out.MediaURLs[0].URL
	if _, ok := imageURL(u); !ok {
		return nil, "", "", fmt.Errorf("%w: media_urls[0] is not an absolute http(s) URL: %q", ErrNoImage, excerpt([]byte(u)))
	}
	b, err := r.fetchImage(ctx, u)
	if err != nil {
		return nil, "", "", err
	}
	ct, isImage, err := imageType(b)
	if err != nil {
		return nil, "", "", err
	}
	if !isImage {
		return nil, "", "", fmt.Errorf("%w: %s answered with %d bytes that are not an image", ErrNoImage, u, len(b))
	}
	return b, ct, u, nil
}

// hasOutcome reports whether the envelope carried a result: an
// `outcome` key whose value is present and not null. `outcome: null`
// is a queued record (t6b-live-record.md) — it has no result, and
// walking the rest of the body could only find the request's own
// echo.
func hasOutcome(raw json.RawMessage) bool {
	t := string(bytes.TrimSpace(raw))
	return t != "" && t != "null"
}

// sentImages is what the request itself put on the wire, and
// therefore what its record's payload echo can repeat: the reference
// sheet bytes an EditImage attached, and the reference sheet URLs.
// A candidate equal to any of them is the request coming back, never
// the result (t6-round1.md H1).
type sentImages struct {
	bytes [][]byte
	urls  []string
}

// hasBytes reports whether b is one of the images this call sent.
func (s sentImages) hasBytes(b []byte) bool {
	for _, v := range s.bytes {
		if bytes.Equal(b, v) {
			return true
		}
	}
	return false
}

// hasURL reports whether u is one of the URLs this call sent.
func (s sentImages) hasURL(u string) bool {
	for _, v := range s.urls {
		if u == v {
			return true
		}
	}
	return false
}

// fetchImage GETs one image URL and returns its bytes. It exists
// because a request-queue result may name a storage.googleapis.com
// object rather than inline it, and those objects are assumed to
// expire (PLAN.md invariant 7) — so the bytes are taken on receipt.
//
// This is not a call to GMI's inference API and does not cross
// PLAN.md invariant 1: the hosts that invariant names are
// api.gmi-serving.com and console.gmicloud.ai, both of which stay
// behind internal/gmi. Only http and https are followed, and the read
// is capped at MaxImageBytes. The default client re-applies the
// scheme check on every hop of a redirect chain and caps the chain
// at maxRedirectHops: a 302 cannot steer the GET at another scheme,
// and a hostile chain cannot outgrow the cap. A same-scheme redirect
// to another host is still followed — the Location comes from the
// authenticated queue's own answer, and a storage edge hop is the
// legitimate case — but only for the bounded number of hops.
// (safeRedirectPolicy; TestFetchImage_RedirectPolicy.)
func (r *renderer) fetchImage(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("illustrate: fetch image %s: %w", rawURL, err)
	}
	resp, err := r.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("illustrate: fetch image %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("illustrate: fetch image %s: %w: HTTP %d", rawURL, ErrNoImage, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, MaxImageBytes+1))
	if err != nil {
		return nil, fmt.Errorf("illustrate: fetch image %s: %w", rawURL, err)
	}
	if len(b) > MaxImageBytes {
		return nil, fmt.Errorf("illustrate: fetch image %s: %w: larger than %d bytes", rawURL, ErrUnsupportedImage, MaxImageBytes)
	}
	return b, nil
}

// imageType sniffs b and reports its content type. ok is false when b
// is not an image at all (JSON, base64 text, anything else), which is
// the signal to keep looking. An image in a type outside
// supportedTypes — a GIF, an SVG, a TIFF — returns
// ErrUnsupportedImage rather than false: it is unmistakably the
// picture, in a form nothing downstream can store or show, and
// stepping over it would end in the far vaguer ErrNoImage.
func imageType(b []byte) (string, bool, error) {
	ct, _, _ := strings.Cut(http.DetectContentType(b), ";")
	ct = strings.TrimSpace(ct)
	if !strings.HasPrefix(ct, "image/") {
		return "", false, nil
	}
	if !supportedTypes[ct] {
		return "", false, fmt.Errorf("%w: %s (want image/png, image/jpeg or image/webp)", ErrUnsupportedImage, ct)
	}
	return ct, true, nil
}

// decodeBase64Image decodes s if it could plausibly carry an image: a
// data: URI's payload, or a long bare base64 string. It reports
// whether anything was decoded, not whether the result is an image —
// the caller sniffs. All four base64 alphabets are tried because the
// encoding is the provider's choice and costs nothing to accept.
func decodeBase64Image(s string) ([]byte, bool) {
	payload := s
	if strings.HasPrefix(strings.ToLower(payload), "data:") {
		_, after, found := strings.Cut(payload, ",")
		if !found {
			return nil, false
		}
		payload = after
	} else if len(payload) < minBase64Len {
		return nil, false
	}
	payload = strings.Map(dropSpace, payload)
	if payload == "" || len(payload) > MaxImageBytes {
		return nil, false
	}
	for _, enc := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		if b, err := enc.DecodeString(payload); err == nil && len(b) > 0 {
			return b, true
		}
	}
	return nil, false
}

// dropSpace is strings.Map's callback for stripping the whitespace a
// wrapped base64 blob carries.
func dropSpace(r rune) rune {
	switch r {
	case ' ', '\t', '\r', '\n':
		return -1
	}
	return r
}

// maxRedirectHops bounds how many redirects fetchImage follows. A
// storage.googleapis.com object may hop once to its serving edge;
// three is generous for that and short of anything a hostile
// response could use to walk the request around the local network.
const maxRedirectHops = 3

// safeRedirectPolicy is the default client's CheckRedirect: every
// hop must be http or https again, and the chain is capped. The
// default policy follows anything any reachable host answers with —
// a response-supplied URL would then be dereferenceable anywhere the
// box can reach, loopback and link-local included.
func safeRedirectPolicy(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirectHops {
		return fmt.Errorf("illustrate: stopped after %d redirects", maxRedirectHops)
	}
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return fmt.Errorf("illustrate: redirect to %q refused: only http and https are followed", req.URL.Scheme)
	}
	return nil
}

// imageURL reports whether s is an absolute http or https URL, and
// returns it. Other schemes are refused: a data: URI is the base64
// path's business and anything else (file:, gs:) is not something
// this package will dereference.
func imageURL(s string) (string, bool) {
	if len(s) > 2048 {
		return "", false
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return "", false
	}
	switch u.Scheme {
	case "http", "https":
		return s, true
	default:
		return "", false
	}
}

// collectStrings walks a decoded JSON document depth-first and
// appends every string it holds. Object keys are visited in sorted
// order so the same body always yields the same candidate order and
// therefore the same image — Go's map iteration is randomised, and a
// decode that picked a different field per run would be an
// unreproducible book.
func collectStrings(v any, out []string) []string {
	switch t := v.(type) {
	case string:
		return append(out, t)
	case []any:
		for _, e := range t {
			out = collectStrings(e, out)
		}
		return out
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out = collectStrings(t[k], out)
		}
		return out
	default:
		return out
	}
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
