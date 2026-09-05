package illustrate

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// jpegBytes and webpBytes are the other two types the closed set
// accepts; gifBytes is the one it does not, and is the fixture for
// ErrUnsupportedImage.
func jpegBytes() []byte { return append([]byte("\xff\xd8\xff\xe0"), make([]byte, 32)...) }
func webpBytes() []byte {
	b := []byte("RIFF")
	b = append(b, 0x20, 0, 0, 0)
	b = append(b, "WEBPVP8 "...)
	return append(b, make([]byte, 16)...)
}
func gifBytes() []byte { return append([]byte("GIF89a"), make([]byte, 32)...) }

// testRenderer returns a renderer with the package defaults, the way
// Config.resolve builds one.
func testRenderer(t *testing.T) *renderer {
	t.Helper()
	r, err := Config{Imager: &fakeImager{}}.resolve()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return r
}

func TestDecodeImage_Shapes(t *testing.T) {
	png := pngBytes("body")
	b64 := base64.StdEncoding.EncodeToString(png)

	tests := []struct {
		name    string
		raw     []byte
		want    []byte
		wantCT  string
		comment string
	}{
		{
			name:    "raw image bytes pass through",
			raw:     png,
			want:    png,
			wantCT:  "image/png",
			comment: "a model that answers with the picture itself needs no decode",
		},
		{
			name:   "data URI in a queue record",
			raw:    []byte(`{"request_id":"r1","status":"success","outcome":{"image":"data:image/png;base64,` + b64 + `"}}`),
			want:   png,
			wantCT: "image/png",
		},
		{
			name:   "bare base64 in an unknown field name",
			raw:    []byte(`{"status":"success","some_field_nobody_documented":"` + b64 + `"}`),
			want:   png,
			wantCT: "image/png",
			comment: "the magic bytes decide, not the field name, so an " +
				"unpublished schema still works",
		},
		{
			name:   "openai-style data array",
			raw:    []byte(`{"data":[{"b64_json":"` + b64 + `"}]}`),
			want:   png,
			wantCT: "image/png",
		},
		{
			name:   "jpeg",
			raw:    []byte(`{"outcome":{"image":"data:image/jpeg;base64,` + base64.StdEncoding.EncodeToString(jpegBytes()) + `"}}`),
			want:   jpegBytes(),
			wantCT: "image/jpeg",
		},
		{
			name:   "webp",
			raw:    []byte(`{"outcome":{"image":"data:image/webp;base64,` + base64.StdEncoding.EncodeToString(webpBytes()) + `"}}`),
			want:   webpBytes(),
			wantCT: "image/webp",
		},
		{
			name:   "raw-url base64 alphabet",
			raw:    []byte(`{"image":"` + base64.RawURLEncoding.EncodeToString(pngBytes("urlsafe-padding-free-payload-long-enough-to-try")) + `"}`),
			want:   pngBytes("urlsafe-padding-free-payload-long-enough-to-try"),
			wantCT: "image/png",
		},
		{
			name:    "request ids are never mistaken for images",
			raw:     []byte(`{"request_id":"` + b64 + `x","status":"success","outcome":{"image":"data:image/png;base64,` + b64 + `"}}`),
			want:    png,
			wantCT:  "image/png",
			comment: "a long id that is not an image must be stepped over, not returned",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := testRenderer(t)
			got, ct, err := r.decodeImage(context.Background(), tc.raw)
			if err != nil {
				t.Fatalf("decodeImage: %v", err)
			}
			if string(got) != string(tc.want) {
				t.Errorf("bytes = %q, want %q", got, tc.want)
			}
			if ct != tc.wantCT {
				t.Errorf("content type = %q, want %q", ct, tc.wantCT)
			}
		})
	}
}

// TestDecodeImage_FetchesAURL covers the live-confirmed shape: the
// request queue answered a completed TTS request with
// {"request_id":...,"status":"success","outcome":{"audio_url":"https://storage.googleapis.com/..."}}.
// Images follow the same envelope, so the bytes have to be taken now
// — those links expire (PLAN.md invariant 7).
func TestDecodeImage_FetchesAURL(t *testing.T) {
	png := pngBytes("fetched-from-storage")
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
	}))
	defer srv.Close()

	raw, err := json.Marshal(map[string]any{
		"request_id": "r1",
		"status":     "success",
		"outcome":    map[string]any{"image_url": srv.URL + "/page.png"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Config.HTTPClient deliberately left nil: this exercises the
	// default client through its default path.
	r := testRenderer(t)
	got, ct, err := r.decodeImage(context.Background(), raw)
	if err != nil {
		t.Fatalf("decodeImage: %v", err)
	}
	if string(got) != string(png) {
		t.Errorf("bytes = %q, want the fetched image", got)
	}
	if ct != "image/png" {
		t.Errorf("content type = %q, want image/png", ct)
	}
	if hits != 1 {
		t.Errorf("upstream fetched %d times, want 1", hits)
	}
}

// TestDecodeImage_InlineBeatsURL pins the preference order: if the
// bytes are already in the body there is no reason to spend a round
// trip on a link that may already have expired.
func TestDecodeImage_InlineBeatsURL(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write(pngBytes("from-url"))
	}))
	defer srv.Close()

	inline := pngBytes("inline")
	raw := []byte(`{"aaa_url":"` + srv.URL + `/x.png","zzz_image":"data:image/png;base64,` +
		base64.StdEncoding.EncodeToString(inline) + `"}`)
	r := testRenderer(t)
	got, _, err := r.decodeImage(context.Background(), raw)
	if err != nil {
		t.Fatalf("decodeImage: %v", err)
	}
	if string(got) != string(inline) {
		t.Errorf("bytes = %q, want the inline image", got)
	}
	if hits != 0 {
		t.Errorf("fetched a URL %d time(s) despite an inline image being present", hits)
	}
}

// TestDecodeImage_IsDeterministic pins the sorted walk. Go randomises
// map iteration, so an unsorted walk would pick a different candidate
// per run and the same book would render differently every time.
func TestDecodeImage_IsDeterministic(t *testing.T) {
	first := pngBytes("aaa")
	second := pngBytes("zzz")
	raw := []byte(`{"zzz":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(second) +
		`","aaa":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(first) + `"}`)
	r := testRenderer(t)
	for i := 0; i < 50; i++ {
		got, _, err := r.decodeImage(context.Background(), raw)
		if err != nil {
			t.Fatalf("decodeImage: %v", err)
		}
		if string(got) != string(first) {
			t.Fatalf("run %d picked %q, want the first key in sorted order (%q)", i, got, first)
		}
	}
}

func TestDecodeImage_ErrorBranches(t *testing.T) {
	gifB64 := base64.StdEncoding.EncodeToString(gifBytes())
	tests := []struct {
		name string
		raw  []byte
		want error
	}{
		{"empty body", nil, ErrNoImage},
		{"neither image nor JSON", []byte("upstream exploded, sorry"), ErrNoImage},
		{"JSON with no image anywhere", []byte(`{"request_id":"r1","status":"success","outcome":{}}`), ErrNoImage},
		{"JSON array with nothing usable", []byte(`[1,2,3,null,true]`), ErrNoImage},
		{"raw gif bytes", gifBytes(), ErrUnsupportedImage},
		{"gif in a data URI", []byte(`{"image":"data:image/gif;base64,` + gifB64 + `"}`), ErrUnsupportedImage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := testRenderer(t)
			got, ct, err := r.decodeImage(context.Background(), tc.raw)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if got != nil || ct != "" {
				t.Errorf("got (%q, %q) beside a non-nil error, want (nil, \"\")", got, ct)
			}
		})
	}
}

func TestDecodeImage_FetchErrorBranches(t *testing.T) {
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer notFound.Close()

	notAnImage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>an error page</html>"))
	}))
	defer notAnImage.Close()

	aGif := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(gifBytes())
	}))
	defer aGif.Close()

	tooBig := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(pngBytes("x"))
		w.Write(make([]byte, MaxImageBytes))
	}))
	defer tooBig.Close()

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close() // nothing is listening now, so the GET fails at the transport

	tests := []struct {
		name string
		url  string
		want error
	}{
		{"upstream 404", notFound.URL + "/x.png", ErrNoImage},
		{"upstream serves something that is not an image", notAnImage.URL + "/x.png", ErrNoImage},
		{"upstream serves an unsupported image type", aGif.URL + "/x.gif", ErrUnsupportedImage},
		{"upstream serves more than MaxImageBytes", tooBig.URL + "/x.png", ErrUnsupportedImage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := testRenderer(t)
			raw := []byte(`{"outcome":{"image_url":"` + tc.url + `"}}`)
			got, ct, err := r.decodeImage(context.Background(), raw)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if got != nil || ct != "" {
				t.Errorf("got (%q, %q) beside a non-nil error", got, ct)
			}
		})
	}

	t.Run("transport failure", func(t *testing.T) {
		r := testRenderer(t)
		raw := []byte(`{"outcome":{"image_url":"` + deadURL + `/x.png"}}`)
		if _, _, err := r.decodeImage(context.Background(), raw); err == nil {
			t.Fatal("err = nil, want a transport failure")
		}
	})

	t.Run("unbuildable request", func(t *testing.T) {
		r := testRenderer(t)
		// A URL that parses as absolute here but that
		// http.NewRequestWithContext rejects: a control character in
		// the path.
		if _, err := r.fetchImage(context.Background(), "http://example.invalid/\x7f"); err == nil {
			t.Fatal("err = nil, want a request-build failure")
		}
	})

	t.Run("cancelled context", func(t *testing.T) {
		r := testRenderer(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		raw := []byte(`{"outcome":{"image_url":"` + notFound.URL + `/x.png"}}`)
		if _, _, err := r.decodeImage(ctx, raw); !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

// TestDecodeImage_NonImageSchemesAreNotFetched pins that only http
// and https are dereferenced: a gs:// or file:// string in a response
// is data, not an instruction to go and read something.
func TestDecodeImage_NonImageSchemesAreNotFetched(t *testing.T) {
	for _, u := range []string{
		"gs://bucket/object.png",
		"file:///etc/passwd",
		"ftp://example.com/x.png",
		"/relative/path.png",
		"not a url at all",
	} {
		t.Run(u, func(t *testing.T) {
			if got, ok := imageURL(u); ok {
				t.Errorf("imageURL(%q) = %q, true; want false", u, got)
			}
		})
	}
	for _, u := range []string{"http://example.com/x.png", "https://storage.googleapis.com/b/o.png"} {
		t.Run(u, func(t *testing.T) {
			if _, ok := imageURL(u); !ok {
				t.Errorf("imageURL(%q) = false, want true", u)
			}
		})
	}
	t.Run("absurdly long string", func(t *testing.T) {
		if _, ok := imageURL("https://example.com/" + strings.Repeat("a", 4096)); ok {
			t.Error("a 4KB string was treated as a URL")
		}
	})
}

func TestDecodeBase64Image(t *testing.T) {
	png := pngBytes("payload")
	tests := []struct {
		name string
		in   string
		ok   bool
	}{
		{"short string is not tried", "queued", false},
		{"request id is not tried", "req-0123456789", false},
		{"data URI with no comma", "data:image/png;base64" + strings.Repeat("A", 80), false},
		{"whitespace-wrapped base64", wrap(base64.StdEncoding.EncodeToString(png)), true},
		{"empty data URI payload", "data:image/png;base64,", false},
		{"characters outside every base64 alphabet", strings.Repeat("not@base64!at#all ", 8), false},
		// Decodable but not an image. decodeBase64Image reports only
		// that bytes came out; imageType is what rejects them, which
		// is why a decodable string must NOT be treated as a hit here.
		{"decodable text is left for the sniffer to reject", strings.Repeat("notbase64atall", 8), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := decodeBase64Image(tc.in)
			if ok != tc.ok {
				t.Errorf("decodeBase64Image(%.40q) ok = %v, want %v", tc.in, ok, tc.ok)
			}
		})
	}
}

// wrap inserts newlines into s the way a provider that line-wraps
// base64 would.
func wrap(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && i%16 == 0 {
			b.WriteString("\n")
		}
		b.WriteRune(r)
	}
	return b.String()
}

func TestCollectStrings(t *testing.T) {
	var doc any
	if err := json.Unmarshal([]byte(`{"b":"two","a":{"z":"three","y":"one"},"c":[4,"four",null,true]}`), &doc); err != nil {
		t.Fatal(err)
	}
	got := collectStrings(doc, nil)
	want := []string{"one", "three", "two", "four"} // a.y, a.z, b, c[1]
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("collectStrings = %v, want %v (sorted keys, depth first)", got, want)
	}
}

func TestExcerpt(t *testing.T) {
	if got := excerpt([]byte("  short  ")); got != "short" {
		t.Errorf("excerpt = %q, want %q", got, "short")
	}
	long := excerpt([]byte(strings.Repeat("x", 500)))
	if len(long) != 203 || !strings.HasSuffix(long, "...") {
		t.Errorf("excerpt of a long body = %d chars (%q...), want 200 plus an ellipsis", len(long), long[:20])
	}
}

// TestFetchImage_TruncatedBody covers the read-failure branch: the
// upstream promises more bytes than it delivers and drops the
// connection. A half-downloaded picture must be an error, not a
// corrupt page nobody notices until the book renders.
func TestFetchImage_TruncatedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4096")
		w.WriteHeader(http.StatusOK)
		w.Write(pngBytes("truncated"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		panic(http.ErrAbortHandler) // drop the connection mid-body
	}))
	defer srv.Close()
	srv.Config.ErrorLog = discardLog()

	r := testRenderer(t)
	if _, err := r.fetchImage(context.Background(), srv.URL+"/x.png"); err == nil {
		t.Fatal("err = nil, want a read failure on a truncated body")
	}
}

// discardLog silences httptest's server log for the deliberate
// connection abort above; the abort is the fixture, not a fault.
func discardLog() *log.Logger { return log.New(io.Discard, "", 0) }
