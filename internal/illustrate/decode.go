package illustrate

import (
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

// decodeImage turns one request-queue response body into image bytes
// and their content type.
//
// internal/gmi/media hands every response back as raw bytes on
// purpose: the request queue returns a different result schema per
// model and GMI has published none of them, so a typed struct there
// would be a fabrication (PLAN.md §T2, amended at t2-round3.md M3).
// The decode therefore belongs here, in the track that chose the
// model — but this package cannot honestly hard-code an unpublished
// schema either. So the decode is structural rather than schematic
// and never guesses:
//
//  1. A body that is already image bytes is passed through.
//  2. Otherwise the body is parsed as JSON and every string in it is
//     examined, in a deterministic order. The first that is a
//     data: URI or base64 that decodes to real image magic is the
//     image. Magic is the test, not the field name, so a schema this
//     package has never seen still works and a request id can never
//     be mistaken for a picture.
//  3. Failing that, the first http/https URL in the body is fetched
//     — the request queue answers some models with a
//     storage.googleapis.com link, and those links expire, so the
//     bytes are taken now (PLAN.md invariant 7).
//
// A body with no image in it is ErrNoImage; an image in a type
// outside supportedTypes is ErrUnsupportedImage. Neither is ever
// silently swallowed: a page with no picture must fail loudly, since
// the alternative is a book with a hole in it.
func (r *renderer) decodeImage(ctx context.Context, raw []byte) ([]byte, string, error) {
	if len(raw) == 0 {
		return nil, "", fmt.Errorf("%w: the response body was empty", ErrNoImage)
	}
	if ct, ok, err := imageType(raw); err != nil {
		return nil, "", err
	} else if ok {
		return raw, ct, nil
	}

	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, "", fmt.Errorf("%w: the response is neither image bytes nor JSON: %s", ErrNoImage, excerpt(raw))
	}
	strs := collectStrings(doc, nil)

	for _, s := range strs {
		b, ok := decodeBase64Image(s)
		if !ok {
			continue
		}
		ct, isImage, err := imageType(b)
		if err != nil {
			return nil, "", err
		}
		if isImage {
			return b, ct, nil
		}
	}

	for _, s := range strs {
		u, ok := imageURL(s)
		if !ok {
			continue
		}
		b, err := r.fetchImage(ctx, u)
		if err != nil {
			return nil, "", err
		}
		ct, isImage, err := imageType(b)
		if err != nil {
			return nil, "", err
		}
		if !isImage {
			return nil, "", fmt.Errorf("%w: %s answered with %d bytes that are not an image", ErrNoImage, u, len(b))
		}
		return b, ct, nil
	}

	return nil, "", fmt.Errorf("%w: no image, data: URI or image URL in the response: %s", ErrNoImage, excerpt(raw))
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
// is capped at MaxImageBytes.
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
