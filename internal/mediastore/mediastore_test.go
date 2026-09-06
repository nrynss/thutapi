package mediastore

import (
	"bytes"
	"context"
	"errors"
	"hash/crc32"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"log/slog"
	"sync"
	"thutapi/internal/store"
)

// openTestStore builds a store + its backing DB on a throwaway
// directory. Every test gets the same ceremony.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := store.Open(t.Context(), store.Config{Path: filepath.Join(t.TempDir(), "thutapi.db")})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := Open(t.Context(), Config{Dir: filepath.Join(t.TempDir(), "media"), DB: db})
	if err != nil {
		t.Fatalf("open mediastore: %v", err)
	}
	return s
}

// blob returns deterministic bytes of the given length: patterned, so
// a byte-slice assertion cannot pass by accident.
func blob(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i % 251)
	}
	return b
}

// TestPersistAndServeFullBody pins the base contract: persist bytes,
// serve them back byte-for-byte under the stored content type, with
// the immutable-caching headers PLAN.md §T1 asks for on generated
// media.
func TestPersistAndServeFullBody(t *testing.T) {
	s := openTestStore(t)
	data := blob(4096)
	id, err := s.Persist(t.Context(), bytes.NewReader(data), "audio/mpeg")
	if err != nil {
		t.Fatalf("persist: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/media/"+id, nil)
	req.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)

	if got, want := rr.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d (body: %s)", got, want, rr.Body.String())
	}
	if got := rr.Header().Get("Content-Type"); got != "audio/mpeg" {
		t.Fatalf("content-type = %q, want audio/mpeg", got)
	}
	if got := rr.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
		t.Fatalf("cache-control = %q, want immutable media caching", got)
	}
	if got := rr.Header().Get("ETag"); got != `"`+id+`"` {
		t.Fatalf("etag = %q, want quoted id", got)
	}
	if got := rr.Body.Bytes(); !bytes.Equal(got, data) {
		t.Fatalf("body = %d bytes (crc %d), want %d bytes (crc %d)",
			len(got), crc32.ChecksumIEEE(got), len(data), crc32.ChecksumIEEE(data))
	}
}

// TestPersistRejectsUnsupportedContentTypes pins the closed set:
// bytes with an unvetted — or empty — type must never become
// servable, because the handler would serve that type verbatim.
func TestPersistRejectsUnsupportedContentTypes(t *testing.T) {
	s := openTestStore(t)
	cases := []struct {
		name        string
		contentType string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"html", "text/html"},
		{"unsupported video", "video/webm"},
		{"invented", "audio/x-jealous-elephant"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, err := s.Persist(t.Context(), bytes.NewReader(blob(8)), c.contentType)
			if !errors.Is(err, ErrInvalidContentType) {
				t.Fatalf("err = %v, want ErrInvalidContentType", err)
			}
			if id != "" {
				t.Fatalf("persist returned id %q alongside an error", id)
			}
		})
	}
}

// TestPersistAcceptsSupportedTypes pins every type in the closed set
// as accepted — a type silently dropped from the set would break the
// model that produces it.
func TestPersistAcceptsSupportedTypes(t *testing.T) {
	s := openTestStore(t)
	for ct := range supportedTypes {
		t.Run(ct, func(t *testing.T) {
			id, err := s.Persist(t.Context(), bytes.NewReader(blob(8)), ct)
			if err != nil {
				t.Fatalf("persist %q: %v", ct, err)
			}
			m, err := s.db.Media(t.Context(), id)
			if err != nil {
				t.Fatalf("row: %v", err)
			}
			if m.ContentType != ct {
				t.Fatalf("row content type = %q, want %q", m.ContentType, ct)
			}
		})
	}
}

// TestPersistNormalizesContentType: parameters and case are stripped —
// the stored type is the bare media type the handler serves.
func TestPersistNormalizesContentType(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Persist(t.Context(), bytes.NewReader(blob(8)), "IMAGE/PNG; charset=binary")
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	m, err := s.db.Media(t.Context(), id)
	if err != nil {
		t.Fatalf("row: %v", err)
	}
	if m.ContentType != "image/png" {
		t.Fatalf("content type = %q, want image/png", m.ContentType)
	}
}

// TestPersistIDsAreUnguessableAndDistinct: the shareability of a
// /media/ URL rests on ids being 128 bits of crypto/rand (PLAN.md
// §T3).
func TestPersistIDsAreUnguessableAndDistinct(t *testing.T) {
	s := openTestStore(t)
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		id, err := s.Persist(t.Context(), bytes.NewReader(blob(4)), "image/png")
		if err != nil {
			t.Fatalf("persist: %v", err)
		}
		if !validID(id) {
			t.Fatalf("id %q is not a 32-char lowercase hex string", id)
		}
		if seen[id] {
			t.Fatalf("persist repeated id %q", id)
		}
		seen[id] = true
	}
}

// TestPersistWithCancelledContextFailsClean: ctx bounds the metadata
// write, and a failed insert must not leave the blob file behind —
// the file only becomes reachable through its row.
func TestPersistWithCancelledContextFailsClean(t *testing.T) {
	s := openTestStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := s.Persist(ctx, bytes.NewReader(blob(8)), "image/png")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	entries, err := fs.ReadDir(os.DirFS(s.dir), ".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("dir has %d entries after failed persist, want none", len(entries))
	}
}

// TestServeHTTPRangeRequests is the pin PLAN.md §T3 asks for: a Range
// header on a stored blob returns 206 with exactly the requested
// bytes — the property <audio> scrubbing in the flipbook depends on.
func TestServeHTTPRangeRequests(t *testing.T) {
	s := openTestStore(t)
	data := blob(1024)
	id, err := s.Persist(t.Context(), bytes.NewReader(data), "audio/mpeg")
	if err != nil {
		t.Fatalf("persist: %v", err)
	}

	cases := []struct {
		name          string
		rangeHeader   string
		wantStatus    int
		wantSlice     []byte
		wantContRange string
	}{
		{"leading bytes", "bytes=0-0", http.StatusPartialContent, data[0:1], "bytes 0-0/1024"},
		{"middle range", "bytes=2-5", http.StatusPartialContent, data[2:6], "bytes 2-5/1024"},
		{"open-ended suffix range", "bytes=1000-", http.StatusPartialContent, data[1000:], "bytes 1000-1023/1024"},
		{"tail range", "bytes=-5", http.StatusPartialContent, data[1019:], "bytes 1019-1023/1024"},
		{"full range", "bytes=0-", http.StatusPartialContent, data, "bytes 0-1023/1024"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/media/"+id, nil)
			req.Header.Set("Range", c.rangeHeader)
			req.SetPathValue("id", id)
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, req)

			if got := rr.Code; got != c.wantStatus {
				t.Fatalf("status = %d, want %d", got, c.wantStatus)
			}
			if got := rr.Header().Get("Content-Range"); got != c.wantContRange {
				t.Fatalf("content-range = %q, want %q", got, c.wantContRange)
			}
			if got := rr.Header().Get("Content-Type"); got != "audio/mpeg" {
				t.Fatalf("content-type = %q, want audio/mpeg (the stored type)", got)
			}
			if got := rr.Body.Bytes(); !bytes.Equal(got, c.wantSlice) {
				t.Fatalf("body = %d bytes, want the %d requested bytes",
					len(got), len(c.wantSlice))
			}
		})
	}
}

// TestPersistAndServeVideoMP4_RangeRequest pins T10d's contract: an MP4
// video blob persists, and GET /media/{id} serves it back with Content-Type
// video/mp4, immutable caching headers, ETag, and functional Range requests
// (both sub-slice and suffix range) required for <video> seeking and scrubbing.
func TestPersistAndServeVideoMP4_RangeRequest(t *testing.T) {
	s := openTestStore(t)
	data := blob(8192)
	id, err := s.Persist(t.Context(), bytes.NewReader(data), "video/mp4")
	if err != nil {
		t.Fatalf("persist video/mp4: %v", err)
	}

	// 1. Full GET returns 200 OK, Content-Type video/mp4, immutable cache-control,
	// quoted ETag, and the full body.
	{
		req := httptest.NewRequest(http.MethodGet, "/media/"+id, nil)
		req.SetPathValue("id", id)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("full GET status = %d, want %d (body: %s)", got, want, rr.Body.String())
		}
		if got := rr.Header().Get("Content-Type"); got != "video/mp4" {
			t.Fatalf("content-type = %q, want video/mp4", got)
		}
		if got := rr.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
			t.Fatalf("cache-control = %q, want immutable media caching", got)
		}
		if got := rr.Header().Get("ETag"); got != `"`+id+`"` {
			t.Fatalf("etag = %q, want quoted id", got)
		}
		if got := rr.Body.Bytes(); !bytes.Equal(got, data) {
			t.Fatalf("body len = %d (crc %d), want %d (crc %d)",
				len(got), crc32.ChecksumIEEE(got), len(data), crc32.ChecksumIEEE(data))
		}
	}

	// 2. Sub-slice Range request: Range: bytes=1024-2047 returns HTTP 206 Partial Content,
	// Content-Type video/mp4, Content-Range bytes 1024-2047/8192, and exact slice data[1024:2048].
	{
		req := httptest.NewRequest(http.MethodGet, "/media/"+id, nil)
		req.Header.Set("Range", "bytes=1024-2047")
		req.SetPathValue("id", id)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusPartialContent; got != want {
			t.Fatalf("range status = %d, want %d", got, want)
		}
		if got := rr.Header().Get("Content-Type"); got != "video/mp4" {
			t.Fatalf("range content-type = %q, want video/mp4", got)
		}
		if got, want := rr.Header().Get("Content-Range"), "bytes 1024-2047/8192"; got != want {
			t.Fatalf("content-range = %q, want %q", got, want)
		}
		wantSlice := data[1024:2048]
		if got := rr.Body.Bytes(); !bytes.Equal(got, wantSlice) {
			t.Fatalf("range body len = %d, want %d bytes (crc %d vs %d)",
				len(got), len(wantSlice), crc32.ChecksumIEEE(got), crc32.ChecksumIEEE(wantSlice))
		}
	}

	// 3. Suffix Range request: Range: bytes=-512 returns HTTP 206 Partial Content,
	// Content-Type video/mp4, Content-Range bytes 7680-8191/8192, and exact slice data[7680:8192].
	{
		req := httptest.NewRequest(http.MethodGet, "/media/"+id, nil)
		req.Header.Set("Range", "bytes=-512")
		req.SetPathValue("id", id)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusPartialContent; got != want {
			t.Fatalf("suffix range status = %d, want %d", got, want)
		}
		if got := rr.Header().Get("Content-Type"); got != "video/mp4" {
			t.Fatalf("suffix range content-type = %q, want video/mp4", got)
		}
		if got, want := rr.Header().Get("Content-Range"), "bytes 7680-8191/8192"; got != want {
			t.Fatalf("content-range = %q, want %q", got, want)
		}
		wantSlice := data[7680:8192]
		if got := rr.Body.Bytes(); !bytes.Equal(got, wantSlice) {
			t.Fatalf("suffix range body len = %d, want %d bytes (crc %d vs %d)",
				len(got), len(wantSlice), crc32.ChecksumIEEE(got), crc32.ChecksumIEEE(wantSlice))
		}
	}
}

// TestPersistAndServePDF_RangeRequest pins T10f's contract: an application/pdf
// blob persists, and GET /media/{id} serves it back with Content-Type
// application/pdf, immutable caching headers, ETag, and functional Range requests
// (both sub-slice and suffix range) required for PDF readers and downloads.
func TestPersistAndServePDF_RangeRequest(t *testing.T) {
	s := openTestStore(t)
	data := blob(8192)
	id, err := s.Persist(t.Context(), bytes.NewReader(data), "application/pdf")
	if err != nil {
		t.Fatalf("persist application/pdf: %v", err)
	}

	// 1. Full GET returns 200 OK, Content-Type application/pdf, immutable cache-control,
	// quoted ETag, and the full body.
	{
		req := httptest.NewRequest(http.MethodGet, "/media/"+id, nil)
		req.SetPathValue("id", id)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusOK; got != want {
			t.Fatalf("full GET status = %d, want %d (body: %s)", got, want, rr.Body.String())
		}
		if got := rr.Header().Get("Content-Type"); got != "application/pdf" {
			t.Fatalf("content-type = %q, want application/pdf", got)
		}
		if got := rr.Header().Get("Cache-Control"); !strings.Contains(got, "immutable") {
			t.Fatalf("cache-control = %q, want immutable media caching", got)
		}
		if got := rr.Header().Get("ETag"); got != `"`+id+`"` {
			t.Fatalf("etag = %q, want quoted id", got)
		}
		if got := rr.Body.Bytes(); !bytes.Equal(got, data) {
			t.Fatalf("body len = %d (crc %d), want %d (crc %d)",
				len(got), crc32.ChecksumIEEE(got), len(data), crc32.ChecksumIEEE(data))
		}
	}

	// 2. Sub-slice Range request: Range: bytes=1024-2047 returns HTTP 206 Partial Content,
	// Content-Type application/pdf, Content-Range bytes 1024-2047/8192, and exact slice data[1024:2048].
	{
		req := httptest.NewRequest(http.MethodGet, "/media/"+id, nil)
		req.Header.Set("Range", "bytes=1024-2047")
		req.SetPathValue("id", id)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusPartialContent; got != want {
			t.Fatalf("range status = %d, want %d", got, want)
		}
		if got := rr.Header().Get("Content-Type"); got != "application/pdf" {
			t.Fatalf("range content-type = %q, want application/pdf", got)
		}
		if got, want := rr.Header().Get("Content-Range"), "bytes 1024-2047/8192"; got != want {
			t.Fatalf("content-range = %q, want %q", got, want)
		}
		wantSlice := data[1024:2048]
		if got := rr.Body.Bytes(); !bytes.Equal(got, wantSlice) {
			t.Fatalf("range body len = %d, want %d bytes (crc %d vs %d)",
				len(got), len(wantSlice), crc32.ChecksumIEEE(got), crc32.ChecksumIEEE(wantSlice))
		}
	}

	// 3. Suffix Range request: Range: bytes=-512 returns HTTP 206 Partial Content,
	// Content-Type application/pdf, Content-Range bytes 7680-8191/8192, and exact slice data[7680:8192].
	{
		req := httptest.NewRequest(http.MethodGet, "/media/"+id, nil)
		req.Header.Set("Range", "bytes=-512")
		req.SetPathValue("id", id)
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)

		if got, want := rr.Code, http.StatusPartialContent; got != want {
			t.Fatalf("suffix range status = %d, want %d", got, want)
		}
		if got := rr.Header().Get("Content-Type"); got != "application/pdf" {
			t.Fatalf("suffix range content-type = %q, want application/pdf", got)
		}
		if got, want := rr.Header().Get("Content-Range"), "bytes 7680-8191/8192"; got != want {
			t.Fatalf("content-range = %q, want %q", got, want)
		}
		wantSlice := data[7680:8192]
		if got := rr.Body.Bytes(); !bytes.Equal(got, wantSlice) {
			t.Fatalf("suffix range body len = %d, want %d bytes (crc %d vs %d)",
				len(got), len(wantSlice), crc32.ChecksumIEEE(got), crc32.ChecksumIEEE(wantSlice))
		}
	}
}

// TestServeHTTPIfNoneMatchPins304: media is immutable, so a matching
// ETag answers 304 and saves the bytes entirely.
func TestServeHTTPIfNoneMatchPins304(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Persist(t.Context(), bytes.NewReader(blob(16)), "image/png")
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/media/"+id, nil)
	req.Header.Set("If-None-Match", `"`+id+`"`)
	req.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if got, want := rr.Code, http.StatusNotModified; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

// TestServeHTTPUnknownAndMalformedIDs: both a well-formed id with no
// row and a malformed id are 404 — the malformed check is also what
// keeps crafted path values from ever reaching the file layer.
func TestServeHTTPUnknownAndMalformedIDs(t *testing.T) {
	s := openTestStore(t)
	cases := []string{
		"00000000000000000000000000000000", // well-formed, no row
		"short",
		"../etc/passwd",
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", // uppercase is not our id shape
		"000000000000000000000000000000zz", // non-hex tail
	}
	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/media/"+id, nil)
			req.SetPathValue("id", id)
			rr := httptest.NewRecorder()
			s.ServeHTTP(rr, req)
			if got, want := rr.Code, http.StatusNotFound; got != want {
				t.Fatalf("status = %d, want %d", got, want)
			}
		})
	}
}

// TestServeHTTPRejectsNonGETMethods pins the method gate on the
// handler itself, not only the mux route.
func TestServeHTTPRejectsNonGETMethods(t *testing.T) {
	s := openTestStore(t)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		req := httptest.NewRequest(method, "/media/00000000000000000000000000000000", nil)
		req.SetPathValue("id", "00000000000000000000000000000000")
		rr := httptest.NewRecorder()
		s.ServeHTTP(rr, req)
		if got, want := rr.Code, http.StatusMethodNotAllowed; got != want {
			t.Fatalf("%s status = %d, want %d", method, got, want)
		}
		if got, want := rr.Header().Get("Allow"), "GET, HEAD"; got != want {
			t.Fatalf("allow = %q, want %q", got, want)
		}
	}
}

// TestServeHTTPRowWithoutFileIs404: a row whose file vanished (crash
// between file and row deletion) answers 404 — there is nothing else
// an honest server can say.
func TestServeHTTPRowWithoutFileIs404(t *testing.T) {
	s := openTestStore(t)
	id := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := s.db.CreateMedia(t.Context(), store.Media{ID: id, ContentType: "image/png", SizeBytes: 3}); err != nil {
		t.Fatalf("create row: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/media/"+id, nil)
	req.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if got, want := rr.Code, http.StatusNotFound; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
}

// TestDeleteRemovesRowAndFile: after Delete, both the metadata and
// the bytes are gone — and a delete of an already-gone file is not an
// error (the row went first by design).
func TestDeleteRemovesRowAndFile(t *testing.T) {
	s := openTestStore(t)
	id, err := s.Persist(t.Context(), bytes.NewReader(blob(8)), "image/png")
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if err := s.Delete(t.Context(), id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.db.Media(t.Context(), id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("row after delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, id)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("file after delete: %v", err)
	}
	if err := s.Delete(t.Context(), id); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("second delete err = %v, want store.ErrNotFound", err)
	}
}

func TestPersistWithIDAndDeleteIfPresent(t *testing.T) {
	s := openTestStore(t)
	id := strings.Repeat("a", 32)
	if err := s.PersistWithID(t.Context(), id, bytes.NewReader(blob(8)), "audio/mpeg"); err != nil {
		t.Fatalf("persist with id: %v", err)
	}
	if _, err := s.db.Media(t.Context(), id); err != nil {
		t.Fatalf("persist with id row: %v", err)
	}
	if err := s.DeleteIfPresent(t.Context(), id); err != nil {
		t.Fatalf("delete if present: %v", err)
	}
	if err := s.DeleteIfPresent(t.Context(), id); err != nil {
		t.Fatalf("delete if present missing: %v", err)
	}
	if err := s.PersistWithID(t.Context(), "../escape", bytes.NewReader(blob(8)), "audio/mpeg"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("malformed id error = %v, want ErrNotFound", err)
	}
}

// TestOpenValidations: a store without a directory or without a
// database is unusable and says so.
func TestOpenValidations(t *testing.T) {
	ctx := t.Context()
	if _, err := Open(ctx, Config{DB: &store.DB{}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty dir err = %v, want ErrInvalid", err)
	}
	if _, err := Open(ctx, Config{Dir: t.TempDir()}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil db err = %v, want ErrInvalid", err)
	}
}

// TestOpenCreatesDir: first boot brings its own media directory up.
func TestOpenCreatesDir(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, store.Config{Path: filepath.Join(t.TempDir(), "thutapi.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	dir := filepath.Join(t.TempDir(), "nested", "media")
	if _, err := Open(ctx, Config{Dir: dir, DB: db}); err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("dir after open: %v", err)
	}
}

// TestValidID pins the shape check ServeHTTP trusts: exactly 32
// lowercase hex characters.
func TestValidID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"0123456789abcdef0123456789abcdef", true},
		{"", false},
		{"0123456789abcdef", false}, // too short
		{"0123456789abcdef0123456789abcde", false},              // 31 chars
		{"0123456789abcdef0123456789abcdef0", false},            // 33 chars
		{"0123456789ABCDEF0123456789ABCDEF", false},             // uppercase
		{"0123456789abcdef0123456789abcdeg", false},             // non-hex
		{"0123456789abcdef/0123456789abcdef", false},            // separator
		{"../../../../etc/passwd\x00\x00\x00\x00\x00aa", false}, // traversal
	}
	for _, c := range cases {
		if got := validID(c.id); got != c.want {
			t.Errorf("validID(%q) = %v, want %v", c.id, got, c.want)
		}
	}
}

// midStreamError fails after delivering n bytes — a truncated source
// (e.g. a GMI download cut off mid-body) must fail the persist, not
// store a partial blob.
type midStreamError struct{ n int }

func (m *midStreamError) Read(p []byte) (int, error) {
	if m.n <= 0 {
		return 0, errors.New("source died")
	}
	give := min(m.n, len(p))
	for i := range give {
		p[i] = byte(i)
	}
	m.n -= give
	return give, nil
}

// TestPersistWithTruncatedSourceFailsWithoutOrphans: a read error
// mid-copy aborts the persist and leaves neither row nor file.
func TestPersistWithTruncatedSourceFailsWithoutOrphans(t *testing.T) {
	s := openTestStore(t)
	_, err := s.Persist(t.Context(), &midStreamError{n: 4}, "image/png")
	if err == nil || errors.Is(err, store.ErrNotFound) {
		t.Fatalf("err = %v, want the copy failure", err)
	}
	if _, err := os.Stat(s.dir); err != nil {
		t.Fatalf("dir: %v", err)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("dir has %d entries after failed persist, want none", len(entries))
	}
}

// TestPersistIntoReadOnlyDirFails: when the blob file cannot be
// created, the persist fails instead of pretending. Skipped under
// root, which ignores directory permissions.
func TestPersistIntoReadOnlyDirFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s := openTestStore(t)
	if err := os.Chmod(s.dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(s.dir, 0o755) })
	_, err := s.Persist(t.Context(), bytes.NewReader(blob(8)), "image/png")
	if err == nil {
		t.Fatal("persist into a read-only directory succeeded")
	}
}

// TestNewIDFromFailsWithoutEntropy pins the id-generation failure
// wrap.
func TestNewIDFromFailsWithoutEntropy(t *testing.T) {
	if _, err := newIDFrom(&midStreamError{n: 0}); err == nil {
		t.Fatal("newIDFrom succeeded with no entropy")
	}
}

// TestOpenOnAFileFails: a directory path that is actually a regular
// file cannot host blobs.
func TestOpenOnAFileFails(t *testing.T) {
	ctx := t.Context()
	db, err := store.Open(ctx, store.Config{Path: filepath.Join(t.TempDir(), "thutapi.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	file := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, Config{Dir: file, DB: db}); err == nil {
		t.Fatal("open on a regular file succeeded")
	}
}

// capture is a slog handler recording record messages: the
// operator-log branches (AGENTS.md §Testing rule 3) are observable
// only through them.
type capture struct {
	mu       sync.Mutex
	messages []string
}

func (c *capture) Enabled(context.Context, slog.Level) bool { return true }

func (c *capture) Handle(_ context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.messages = append(c.messages, r.Message)
	return nil
}

func (c *capture) WithAttrs([]slog.Attr) slog.Handler { return c }

func (c *capture) WithGroup(string) slog.Handler { return c }

func (c *capture) has(message string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, m := range c.messages {
		if m == message {
			return true
		}
	}
	return false
}

// failingBlob serves real writes through an underlying file while
// failing the stages a test injects — the fault injection behind the
// writeBlob sync- and close-failure pins.
type failingBlob struct {
	*os.File
	failSync  error
	failClose error
}

func (f *failingBlob) Sync() error {
	if f.failSync != nil {
		return f.failSync
	}
	return f.File.Sync()
}

func (f *failingBlob) Close() error {
	if f.failClose != nil {
		return f.failClose
	}
	return f.File.Close()
}

// emptyDir fails the test unless the blob directory holds nothing —
// the observable of "the partial file was removed".
func emptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		t.Fatalf("blob dir holds %q after the failed persist, want it removed", e.Name())
	}
}

// TestNotFoundSentinelContract pins finding 1's fix: every not-found
// path the ErrNotFound doc names matches the package sentinel and —
// wherever the missing row originates in the store — store.ErrNotFound
// too, so a consumer following either contract classifies correctly.
func TestNotFoundSentinelContract(t *testing.T) {
	s := openTestStore(t)
	const unknown = "ffffffffffffffffffffffffffffffff"
	cases := []struct {
		name          string
		probe         func(t *testing.T) error
		matchStoreToo bool
	}{
		{"delete unknown id", func(t *testing.T) error {
			return s.Delete(t.Context(), unknown)
		}, true},
		{"delete malformed id", func(t *testing.T) error {
			return s.Delete(t.Context(), "../secrets")
		}, false},
		{"media unknown id", func(t *testing.T) error {
			_, err := s.media(t.Context(), unknown)
			return err
		}, true},
		{"media malformed id", func(t *testing.T) error {
			_, err := s.media(t.Context(), "0x")
			return err
		}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.probe(t)
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("err = %v, want match on mediastore.ErrNotFound", err)
			}
			if c.matchStoreToo && !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("err = %v, want match on store.ErrNotFound too", err)
			}
		})
	}
}

// TestServeHTTPWithClosedDBIs500: the metadata lookup failing behind
// the handler is a server fault, not a 404 — the documented answer is
// a 500 and one log line. Deleting the metadata 500 branch turns
// this pin red: the request would ride on and serve the file as if
// nothing were wrong.
func TestServeHTTPWithClosedDBIs500(t *testing.T) {
	s := openTestStore(t)
	logs := &capture{}
	s.log = slog.New(logs)
	id, err := s.Persist(t.Context(), bytes.NewReader(blob(8)), "image/png")
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	// Close the database behind the store's back, like a crashed or
	// stopped server would.
	if err := s.db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/media/"+id, nil)
	req.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if got, want := rr.Code, http.StatusInternalServerError; got != want {
		t.Fatalf("status = %d, want %d (body: %s)", got, want, rr.Body.String())
	}
	if !logs.has("mediastore: metadata lookup failed") {
		t.Fatal("no metadata-lookup-failed line was logged")
	}
}

// TestServeHTTPUnopenableBlobIs500: a row whose file exists but
// cannot be opened is a server fault, not a 404 — the client did
// nothing wrong, and the documented answer is a 500 and one log
// line. The log line is load-bearing in the pin: with the 500 branch
// deleted, ServeContent happens to answer 500 anyway on the unusable
// handle, and only the missing log line turns red. Skipped under
// root, which ignores file permissions.
func TestServeHTTPUnopenableBlobIs500(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	s := openTestStore(t)
	logs := &capture{}
	s.log = slog.New(logs)
	id, err := s.Persist(t.Context(), bytes.NewReader(blob(8)), "image/png")
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if err := os.Chmod(filepath.Join(s.dir, id), 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(filepath.Join(s.dir, id), 0o644) })
	req := httptest.NewRequest(http.MethodGet, "/media/"+id, nil)
	req.SetPathValue("id", id)
	rr := httptest.NewRecorder()
	s.ServeHTTP(rr, req)
	if got, want := rr.Code, http.StatusInternalServerError; got != want {
		t.Fatalf("status = %d, want %d (body: %s)", got, want, rr.Body.String())
	}
	if !logs.has("mediastore: blob open failed") {
		t.Fatal("no blob-open-failed line was logged")
	}
}

// TestPersistSyncFailureRemovesPartialBlob: the fsync stage failing
// — a full disk, a dying volume — must abort the persist and leave
// neither partial file nor row behind.
func TestPersistSyncFailureRemovesPartialBlob(t *testing.T) {
	s := openTestStore(t)
	s.newBlob = func(id string) (blobFile, error) {
		f, err := s.root.Create(id)
		if err != nil {
			return nil, err
		}
		return &failingBlob{File: f, failSync: errors.New("disk refuses to flush")}, nil
	}
	_, err := s.Persist(t.Context(), bytes.NewReader(blob(64)), "image/png")
	if err == nil || !strings.Contains(err.Error(), "sync blob") {
		t.Fatalf("err = %v, want the sync stage failure", err)
	}
	emptyDir(t, s.dir)
}

// TestPersistCloseFailureRemovesPartialBlob: the close stage failing
// after a successful write and sync must abort the persist too —
// the blob is not trusted on disk until Close agrees.
func TestPersistCloseFailureRemovesPartialBlob(t *testing.T) {
	s := openTestStore(t)
	s.newBlob = func(id string) (blobFile, error) {
		f, err := s.root.Create(id)
		if err != nil {
			return nil, err
		}
		return &failingBlob{File: f, failClose: errors.New("close refuses")}, nil
	}
	_, err := s.Persist(t.Context(), bytes.NewReader(blob(64)), "image/png")
	if err == nil || !strings.Contains(err.Error(), "close blob") {
		t.Fatalf("err = %v, want the close stage failure", err)
	}
	emptyDir(t, s.dir)
}

// TestPersistSyncFailureWithFailingRemoveLogsOrphan: when the sync
// fails and the partial file cannot be removed either (the directory
// became unremovable-from mid-persist), the remove failure is one
// log line for the operator, not a swallowed error. Skipped under
// root, which ignores directory permissions.
func TestPersistSyncFailureWithFailingRemoveLogsOrphan(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	s := openTestStore(t)
	logs := &capture{}
	s.log = slog.New(logs)
	s.newBlob = func(id string) (blobFile, error) {
		f, err := s.root.Create(id)
		if err != nil {
			return nil, err
		}
		// The blob file is on disk; make the directory
		// unremovable-from before the injected sync failure sends
		// writeBlob's fail closure after it.
		if err := os.Chmod(s.dir, 0o555); err != nil {
			return nil, err
		}
		return &failingBlob{File: f, failSync: errors.New("disk refuses to flush")}, nil
	}
	t.Cleanup(func() { os.Chmod(s.dir, 0o755) })
	if _, err := s.Persist(t.Context(), bytes.NewReader(blob(64)), "image/png"); err == nil {
		t.Fatal("persist survived a sync failure")
	}
	if !logs.has("mediastore: partial blob not removed") {
		t.Fatal("no partial-blob-not-removed line was logged")
	}
}
