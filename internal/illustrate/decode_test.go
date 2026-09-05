package illustrate

import (
	"bytes"
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
			name:   "data URI under an unknown key",
			raw:    []byte(`{"status":"success","picture":"data:image/png;base64,` + b64 + `"}`),
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
			raw:    []byte(`{"picture":"data:image/jpeg;base64,` + base64.StdEncoding.EncodeToString(jpegBytes()) + `"}`),
			want:   jpegBytes(),
			wantCT: "image/jpeg",
		},
		{
			name:   "webp",
			raw:    []byte(`{"picture":"data:image/webp;base64,` + base64.StdEncoding.EncodeToString(webpBytes()) + `"}`),
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
			raw:     []byte(`{"request_id":"` + b64 + `x","status":"success","picture":"data:image/png;base64,` + b64 + `"}`),
			want:    png,
			wantCT:  "image/png",
			comment: "a long id that is not an image must be stepped over, not returned",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := testRenderer(t)
			got, ct, _, err := r.decodeImage(context.Background(), tc.raw, sentImages{})
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

// TestDecodeImage_FetchesAURL covers the live-confirmed image shape
// (t6b-live-record.md §3): the result is a URL under
// outcome.media_urls — a LIST OF OBJECTS {"id","url"} — beside a
// thumbnail_image_url that is never the picture this call asked for.
// The bytes have to be taken now: those links expire (PLAN.md
// invariant 7).
func TestDecodeImage_FetchesAURL(t *testing.T) {
	png := pngBytes("fetched-from-storage")
	var renderHits, thumbHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/thumb.png") {
			thumbHits++
			w.Write(pngBytes("thumb"))
			return
		}
		renderHits++
		w.Header().Set("Content-Type", "image/png")
		w.Write(png)
	}))
	defer srv.Close()

	raw, err := json.Marshal(map[string]any{
		"request_id": "r1",
		"status":     "success",
		"model":      "seedream-5.0-lite",
		"payload":    map[string]any{"prompt": "a red cube"},
		"outcome": map[string]any{
			"media_urls":          []map[string]string{{"id": "0", "url": srv.URL + "/page.png"}},
			"thumbnail_image_url": srv.URL + "/thumb.png",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Config.HTTPClient deliberately left nil: this exercises the
	// default client through its default path.
	r := testRenderer(t)
	got, ct, url, err := r.decodeImage(context.Background(), raw, sentImages{})
	if err != nil {
		t.Fatalf("decodeImage: %v", err)
	}
	if string(got) != string(png) {
		t.Errorf("bytes = %q, want the fetched image", got)
	}
	if ct != "image/png" {
		t.Errorf("content type = %q, want image/png", ct)
	}
	if url != srv.URL+"/page.png" {
		t.Errorf("url = %q, want the media_urls[0].url the bytes came from", url)
	}
	if renderHits != 1 {
		t.Errorf("render URL fetched %d times, want 1", renderHits)
	}
	if thumbHits != 0 {
		t.Errorf("thumbnail_image_url fetched %d times — the thumbnail must never be picked as the picture", thumbHits)
	}
}

// TestDecodeImage_EnvelopeKeysOnMediaURLs is round 1 H1's pin at the
// decode level: the queue record carries the submitted payload echoed
// verbatim BESIDE the outcome, and for an EditImage call that echo can
// hold the reference sheet as a perfectly decodable image. The decode
// must key on outcome.media_urls by name and never consult the echo —
// the page comes back as the render, never as the sheet it was drawn
// from, in the one shape the live queue is known to produce
// (t6b-live-record.md §3; the polled record is byte-identical in the
// parts that matter).
func TestDecodeImage_EnvelopeKeysOnMediaURLs(t *testing.T) {
	sheet := pngBytes("SHEET-MIRA")
	render := pngBytes("THE-ACTUAL-PAGE")
	var renderHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		renderHits++
		w.Header().Set("Content-Type", "image/png")
		w.Write(render)
	}))
	defer srv.Close()

	// The echo is what EditImage actually sent: sheet bytes inline
	// AND the sheet URL inside payload.image, exactly as the live i2i
	// record repeats the request's image array.
	raw := []byte(`{"request_id":"r1","status":"success","model":"seedream-5.0-lite",` +
		`"payload":{"prompt":"keep Mira identical","image":["` + srv.URL + `/sheet.png"],` +
		`"image_b64":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(sheet) + `"},` +
		`"outcome":{"media_urls":[{"id":"0","url":"` + srv.URL + `/render.png"}],` +
		`"thumbnail_image_url":"` + srv.URL + `/thumb.png"}}`)
	r := testRenderer(t)
	got, _, url, err := r.decodeImage(context.Background(), raw, sentImages{bytes: [][]byte{sheet}, urls: []string{srv.URL + "/sheet.png"}})
	if err != nil {
		t.Fatalf("decodeImage: %v", err)
	}
	if bytes.Equal(got, sheet) {
		t.Error("page came back as the REFERENCE SHEET: the echoed payload.image was decoded as the result")
	}
	if !bytes.Equal(got, render) {
		t.Errorf("bytes = %q, want the render served at media_urls[0].url", got)
	}
	if url != srv.URL+"/render.png" {
		t.Errorf("url = %q, want the media_urls[0].url", url)
	}
	if renderHits != 1 {
		t.Errorf("render URL fetched %d times, want exactly 1", renderHits)
	}
}

// TestDecodeImage_InlineBeatsURL pins the NON-ENVELOPE walk's
// preference order: a body with no outcome subtree is walked base64
// before URLs, so bytes already in the body spare the round trip a
// link costs — and those links expire (PLAN.md invariant 7). A queue
// envelope is a different shape: it decodes from its outcome subtree
// alone (decodeImage step 2) and its payload echo is never
// consulted, so this test pins the structural fallback for schemas
// GMI never published, not the envelope.
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
	got, _, _, err := r.decodeImage(context.Background(), raw, sentImages{})
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

// TestDecodeImage_EchoedPayloadIsSteppedOver pins the walk half of
// the H1 fix: the queue echoes the submitted payload verbatim in its
// record (t6b-live-record.md), and an EditImage echo carries the
// reference sheet as a perfectly decodable PNG. In a body being
// walked, bytes equal to what this call sent are the request coming
// back, never the result.
func TestDecodeImage_EchoedPayloadIsSteppedOver(t *testing.T) {
	sheet := pngBytes("SHEET-MIRA")
	render := pngBytes("THE-ACTUAL-PAGE")
	sheetB64 := base64.StdEncoding.EncodeToString(sheet)
	renderB64 := base64.StdEncoding.EncodeToString(render)

	t.Run("an echo and nothing else is no image at all", func(t *testing.T) {
		raw := []byte(`{"payload":{"image":"data:image/png;base64,` + sheetB64 + `"}}`)
		r := testRenderer(t)
		if _, _, _, err := r.decodeImage(context.Background(), raw, sentImages{bytes: [][]byte{sheet}}); !errors.Is(err, ErrNoImage) {
			t.Fatalf("err = %v, want ErrNoImage: the request's own echo is not a result", err)
		}
	})
	t.Run("the render beside the echo wins", func(t *testing.T) {
		raw := []byte(`{"payload":{"image":"data:image/png;base64,` + sheetB64 +
			`"},"render":"data:image/png;base64,` + renderB64 + `"}`)
		r := testRenderer(t)
		got, _, _, err := r.decodeImage(context.Background(), raw, sentImages{bytes: [][]byte{sheet}})
		if err != nil {
			t.Fatalf("decodeImage: %v", err)
		}
		if string(got) != string(render) {
			t.Errorf("bytes = %q, want the render, not the echoed sheet", got)
		}
	})
	t.Run("nothing sent means nothing excluded", func(t *testing.T) {
		// GenerateImage sends no reference image, so its response
		// carries nothing this call sent and the sheet-shaped answer
		// is simply the answer.
		raw := []byte(`{"payload":{"image":"data:image/png;base64,` + sheetB64 + `"}}`)
		r := testRenderer(t)
		got, _, _, err := r.decodeImage(context.Background(), raw, sentImages{})
		if err != nil {
			t.Fatalf("decodeImage: %v", err)
		}
		if string(got) != string(sheet) {
			t.Errorf("bytes = %q, want the sheet: with no sent payload there is no echo to step over", got)
		}
	})
}

// TestDecodeImage_QueuedRecordHasNoPicture pins the queued shape
// captured live (t6b-live-record.md): `outcome` is null until the
// job is terminal, and the payload beside it echoes the request. A
// record in that state has no picture in it — the decode fails
// loudly instead of handing back the request's own reference sheet.
func TestDecodeImage_QueuedRecordHasNoPicture(t *testing.T) {
	sheet := pngBytes("SHEET-MIRA")
	raw := []byte(`{"request_id":"r1","status":"queued","model":"Flux2-Klein",` +
		`"payload":{"prompt":"Mira","image":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(sheet) + `"},` +
		`"outcome":null}`)
	r := testRenderer(t)
	got, ct, _, err := r.decodeImage(context.Background(), raw, sentImages{bytes: [][]byte{sheet}})
	if !errors.Is(err, ErrNoImage) {
		t.Fatalf("err = %v, want ErrNoImage: a queued record has no result yet", err)
	}
	if got != nil || ct != "" {
		t.Errorf("got (%q, %q) beside the error, want (nil, \"\")", got, ct)
	}
}

// TestDecodeImage_UnsupportedCandidateIsDeferred pins the walk's
// error deferral (t6-round1.md M3): one candidate that sniffs as an
// unsupported type, or one URL that fails, is remembered rather than
// fatal, because a body of unknown shape may carry a usable image
// further on and a failed page fails the whole book. A
// single-candidate body still surfaces the error
// (TestDecodeImage_ErrorBranches' gif row), and inside an outcome
// subtree there is nothing further on to consult, so a bad answer
// there fails immediately.
func TestDecodeImage_UnsupportedCandidateIsDeferred(t *testing.T) {
	render := pngBytes("render")

	t.Run("a gif earlier in the walk does not abort a later png", func(t *testing.T) {
		raw := []byte(`{"aaa_preview":"data:image/gif;base64,` + base64.StdEncoding.EncodeToString(gifBytes()) +
			`","zzz_image":"data:image/png;base64,` + base64.StdEncoding.EncodeToString(render) + `"}`)
		r := testRenderer(t)
		got, ct, _, err := r.decodeImage(context.Background(), raw, sentImages{})
		if err != nil {
			t.Fatalf("decodeImage: %v", err)
		}
		if string(got) != string(render) || ct != "image/png" {
			t.Errorf("got (%q, %q), want the body's png", got, ct)
		}
	})
	t.Run("a failing URL earlier in the walk does not abort a later one", func(t *testing.T) {
		dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "gone", http.StatusNotFound)
		}))
		defer dead.Close()
		good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(render)
		}))
		defer good.Close()
		raw := []byte(`{"aaa_url":"` + dead.URL + `/x.png","zzz_url":"` + good.URL + `/x.png"}`)
		r := testRenderer(t)
		got, ct, _, err := r.decodeImage(context.Background(), raw, sentImages{})
		if err != nil {
			t.Fatalf("decodeImage: %v", err)
		}
		if string(got) != string(render) || ct != "image/png" {
			t.Errorf("got (%q, %q), want the second URL's png", got, ct)
		}
	})
	t.Run("an unsupported image inside the outcome subtree fails immediately", func(t *testing.T) {
		gifSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(gifBytes())
		}))
		defer gifSrv.Close()
		raw := []byte(`{"outcome":{"media_urls":[{"id":"0","url":"` + gifSrv.URL + `/x.gif"}],"thumbnail_image_url":"` + gifSrv.URL + `/x-thumb.png"}}`)
		r := testRenderer(t)
		if _, _, _, err := r.decodeImage(context.Background(), raw, sentImages{}); !errors.Is(err, ErrUnsupportedImage) {
			t.Fatalf("err = %v, want ErrUnsupportedImage: the outcome is the answer, there is nothing further to consult", err)
		}
	})
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
		got, _, _, err := r.decodeImage(context.Background(), raw, sentImages{})
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
		{"outcome with no media_urls", []byte(`{"request_id":"r1","status":"success","outcome":{}}`), ErrNoImage},
		{"outcome with an empty media_urls list", []byte(`{"request_id":"r1","status":"success","outcome":{"media_urls":[]}}`), ErrNoImage},
		{"JSON array with nothing usable", []byte(`[1,2,3,null,true]`), ErrNoImage},
		{"raw gif bytes", gifBytes(), ErrUnsupportedImage},
		{"gif in a data URI", []byte(`{"image":"data:image/gif;base64,` + gifB64 + `"}`), ErrUnsupportedImage},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := testRenderer(t)
			got, ct, _, err := r.decodeImage(context.Background(), tc.raw, sentImages{})
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
			raw := []byte(`{"outcome":{"media_urls":[{"id":"0","url":"` + tc.url + `"}]}}`)
			got, ct, _, err := r.decodeImage(context.Background(), raw, sentImages{})
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
		raw := []byte(`{"outcome":{"media_urls":[{"id":"0","url":"` + deadURL + `/x.png"}]}}`)
		if _, _, _, err := r.decodeImage(context.Background(), raw, sentImages{}); err == nil {
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
		raw := []byte(`{"outcome":{"media_urls":[{"id":"0","url":"` + notFound.URL + `/x.png"}]}}`)
		if _, _, _, err := r.decodeImage(ctx, raw, sentImages{}); !errors.Is(err, context.Canceled) {
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

// TestFetchImage_RedirectPolicy pins the default client's redirect
// policy (t6-round1.md L2): every hop must be http or https again,
// and the chain is capped at maxRedirectHops, so a response-supplied
// Location cannot steer the GET at another scheme or walk an
// unbounded chain. Go's own default policy would follow the
// same-scheme chain below to its tenth hop; the cap is what this
// package adds, and the count is the assertion that bites.
func TestFetchImage_RedirectPolicy(t *testing.T) {
	t.Run("a chain longer than maxRedirectHops is cut", func(t *testing.T) {
		hits := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits++
			http.Redirect(w, r, "/next-hop", http.StatusFound)
		}))
		defer srv.Close()
		r := testRenderer(t)
		_, err := r.fetchImage(context.Background(), srv.URL+"/hop0")
		if err == nil {
			t.Fatal("err = nil, want the redirect chain to be cut")
		}
		if !strings.Contains(err.Error(), "stopped after 3 redirects") {
			t.Errorf("err = %v, want the hop-cap refusal", err)
		}
		if hits != maxRedirectHops+1 {
			t.Errorf("fetched %d hops, want exactly %d — the initial request plus the allowed redirects, never the target of the refused hop", hits, maxRedirectHops+1)
		}
	})
	t.Run("a redirect to a non-http scheme is refused", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		}))
		defer srv.Close()
		r := testRenderer(t)
		if _, err := r.fetchImage(context.Background(), srv.URL+"/x.png"); err == nil {
			t.Fatal("err = nil, want the file: redirect refused")
		}
	})
}
