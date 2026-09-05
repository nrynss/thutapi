// Package mediastore persists generated media — the images and audio
// GMI returns, the MP4 book film, and the printable PDF book — under
// the data dir and serves it back over HTTP.
//
// GMI's response URLs point at storage.googleapis.com and are assumed
// to expire (PLAN.md invariant 7), so the bytes are handed over the
// moment the caller receives them: Persist copies the bytes to disk
// and returns an id, never a URL. Each blob is one file named by its
// id; the metadata row lives in the SQLite media table via
// internal/store. Ids are 128 bits of crypto/rand (see store.NewID),
// so /media/ URLs are shareable without being enumerable (PLAN.md
// §T3).
//
// Store is an http.Handler. Register it once at "GET /media/{id}" in
// cmd/thutapi's newServer (PLAN.md invariant 5); it answers through
// http.ServeContent, so Range requests work — <audio> scrubbing in
// the flipbook depends on it (PLAN.md §T3). Content types are a
// closed set: Persist rejects anything it would not later serve.
//
// Configuration arrives through Config — this package never reads the
// environment (PLAN.md invariant 2) — and errors are sentinels
// matched with errors.Is (invariant 8). Every Persist that returns
// without error survives a process restart; the restart-survival test
// pins it end to end.
package mediastore

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"thutapi/internal/store"
)

// idLen is the length of a media id: 128 bits of entropy,
// hex-encoded — the shape store.NewID produces.
const idLen = 32

// ErrNotFound is returned when a media id has no stored blob: unknown
// id, a malformed one, or a row whose file has vanished. It wraps
// store.ErrNotFound wherever the missing row originates in the
// store, so both sentinels match with errors.Is. A server fault (a
// closed database, a cancelled context) never matches it — "already
// gone" and "broken now" must stay distinguishable.
var ErrNotFound = errors.New("not found")

// ErrInvalidContentType is returned by Persist for an empty or
// unsupported content type. The set is closed on purpose: the handler
// serves the stored type verbatim, so bytes with an unvetted type
// must never make it to disk. Extend the set only when a GMI model
// genuinely returns a new type.
var ErrInvalidContentType = errors.New("mediastore: invalid content type")

// ErrInvalid is returned for unusable configuration: a missing
// directory or a missing store.
var ErrInvalid = errors.New("mediastore: invalid config")

// supportedTypes is the closed content-type set (see
// ErrInvalidContentType): the image types the request-queue image
// models return, Speech 2.8's mp3, wav as the TTS fallback,
// video/mp4 for the book film from internal/bookvideo/bookgen, and
// application/pdf for the printable book PDF from internal/bookpdf/bookgen.
var supportedTypes = map[string]bool{
	"image/png":       true,
	"image/jpeg":      true,
	"image/webp":      true,
	"audio/mpeg":      true,
	"audio/wav":       true,
	"video/mp4":       true,
	"application/pdf": true,
}

// Config configures Open.
type Config struct {
	// Dir is the directory blobs are written under — the media/
	// subdirectory of the data dir. Created on Open if absent.
	Dir string
	// DB is the opened store the metadata rows are written to. Must
	// be non-nil.
	DB *store.DB
	// Log receives one line per server fault (a row whose file cannot
	// be opened). Nil discards.
	Log *slog.Logger
}

// Store persists media blobs on disk, records their metadata via
// internal/store, and serves them over HTTP. Create it with Open; the
// zero value is not usable. Store is safe for concurrent use.
type Store struct {
	dir  string
	root *os.Root
	db   *store.DB
	log  *slog.Logger

	// newBlob creates the blob file named id. Open binds it to the
	// root handle; tests substitute failing file handles to reach
	// writeBlob's sync- and close-failure branches (AGENTS.md
	// §Testing rule 3).
	newBlob func(id string) (blobFile, error)
}

// Open creates the blob directory (if absent) and returns the Store.
// The directory handle is an os.Root, so every blob open and create
// is confined to Dir even if an id were ever crafted to escape it.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	if cfg.Dir == "" {
		return nil, fmt.Errorf("mediastore: open: %w: Dir must not be empty", ErrInvalid)
	}
	if cfg.DB == nil {
		return nil, fmt.Errorf("mediastore: open: %w: DB must not be nil", ErrInvalid)
	}
	if err := os.MkdirAll(cfg.Dir, 0o755); err != nil {
		return nil, fmt.Errorf("mediastore: open: create dir: %w", err)
	}
	root, err := os.OpenRoot(cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("mediastore: open: %w", err)
	}
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Store{dir: cfg.Dir, root: root, db: cfg.DB, log: log}
	s.newBlob = func(id string) (blobFile, error) { return s.root.Create(id) }
	return s, nil
}

// Persist writes src as a new blob and returns its unguessable id.
//
// The content type must be one of the supported types (audio/mpeg,
// image/png, ...); anything else — including empty — is
// ErrInvalidContentType. A type with parameters
// ("image/png; charset=binary") is accepted and stored as its bare
// media type. The blob is fsynced before the metadata row is
// inserted, so a Persist that returns without error survives a
// process restart (PLAN.md §T3's Done when). The copy itself reads
// only from src; ctx bounds the metadata write.
func (s *Store) Persist(ctx context.Context, src io.Reader, contentType string) (string, error) {
	ct, ok := normalizeContentType(contentType)
	if !ok {
		return "", fmt.Errorf("mediastore: persist: %w: %q", ErrInvalidContentType, contentType)
	}
	id, err := newID()
	if err != nil {
		return "", fmt.Errorf("mediastore: persist: %w", err)
	}
	size, err := s.writeBlob(id, src)
	if err != nil {
		return "", fmt.Errorf("mediastore: persist: %w", err)
	}
	_, err = s.db.CreateMedia(ctx, store.Media{ID: id, ContentType: ct, SizeBytes: size})
	if err != nil {
		// No row means the blob is unreachable through the API; drop
		// the file so a failed persist cannot leak disk space. There
		// is no metadata to lose, so best-effort is honest here.
		if rmErr := s.root.Remove(id); rmErr != nil {
			s.log.Error("mediastore: orphaned blob after failed insert", "id", id, "err", rmErr.Error())
		}
		return "", fmt.Errorf("mediastore: persist: %w", err)
	}
	return id, nil
}

// blobFile is the file handle writeBlob writes through: io.Copy's
// Write, the Sync that puts the bytes on disk before the metadata
// row lands, and the Close. *os.File is the production
// implementation; the seam exists so the never-expected sync- and
// close-failure branches can be fault-injected in tests.
type blobFile interface {
	io.WriteCloser
	Sync() error
}

// writeBlob copies src into the file named id and fsyncs before
// close, so the bytes are on disk when Persist returns. On any
// failure the partial file is removed: a blob that was never
// inserted must not leak disk space.
func (s *Store) writeBlob(id string, src io.Reader) (int64, error) {
	f, err := s.newBlob(id)
	if err != nil {
		return 0, fmt.Errorf("create blob: %w", err)
	}
	// fail gives up on the blob: nothing it wrote is reachable (the
	// row comes later), so the partial file is removed before
	// returning. The close here is best effort — the file's next stop
	// is Remove, and a close error on a failing write changes nothing
	// about that.
	fail := func(stage string, err error) (int64, error) {
		f.Close() // best effort: the write already failed and the file is about to be removed
		if rmErr := s.root.Remove(id); rmErr != nil {
			s.log.Error("mediastore: partial blob not removed", "id", id, "stage", stage, "err", rmErr.Error())
		}
		return 0, fmt.Errorf("%s blob: %w", stage, err)
	}
	size, err := io.Copy(f, src)
	if err != nil {
		return fail("write", err)
	}
	if err := f.Sync(); err != nil {
		return fail("sync", err)
	}
	if err := f.Close(); err != nil {
		return fail("close", err)
	}
	return size, nil
}

// Delete removes the blob's metadata row and its file. The row goes
// first, so a crash between the two leaves an unreferenced file —
// exactly what PLAN.md §T11's retention sweep owns cleaning up.
// Unknown or malformed ids return an error matching ErrNotFound
// (which wraps store.ErrNotFound).
func (s *Store) Delete(ctx context.Context, id string) error {
	if err := s.db.DeleteMedia(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return notFound(id, fmt.Errorf("delete: %w", err))
		}
		return fmt.Errorf("mediastore: delete: %w", err)
	}
	// The file is already unreachable once the row is gone, so a
	// failure here is a log, not a caller error.
	if err := s.root.Remove(id); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.log.Error("mediastore: unreferenced blob not removed", "id", id, "err", err.Error())
	}
	return nil
}

// notFound classifies err as the package's ErrNotFound for id while
// keeping err in the chain — store.ErrNotFound where the missing row
// originates, fs.ErrNotExist for a vanished file — so a consumer can
// match either sentinel with errors.Is.
func notFound(id string, err error) error {
	return fmt.Errorf("mediastore: media %s: %w: %w", id, ErrNotFound, err)
}

// media resolves one media id to its metadata row: the lookup
// ServeHTTP's classification runs on. Every not-found shape the
// ErrNotFound doc names — a malformed id, an unknown one — matches
// ErrNotFound; any other failure (a closed database, a cancelled
// context) matches no sentinel, so "already gone" can never be
// confused with a server fault.
func (s *Store) media(ctx context.Context, id string) (store.Media, error) {
	if !validID(id) {
		return store.Media{}, fmt.Errorf("mediastore: media %q: %w: malformed id", id, ErrNotFound)
	}
	m, err := s.db.Media(ctx, id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return store.Media{}, notFound(id, err)
		}
		return store.Media{}, fmt.Errorf("mediastore: media %s: %w", id, err)
	}
	return m, nil
}

// ServeHTTP serves one stored blob at GET or HEAD /media/{id}.
// Registered by main's newServer (PLAN.md invariant 5). http.
// ServeContent does the protocol work — Range, If-None-Match via the
// ETag, HEAD — so audio scrubbing over a Range request gets correct
// 206 answers. Unknown, malformed or file-less ids are 404 — every
// not-found shape classifies as ErrNotFound (see media); a blob
// that exists but cannot be opened is a 500 and one log line.
func (s *Store) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.PathValue("id")
	m, err := s.media(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.log.Error("mediastore: metadata lookup failed", "id", id, "err", err.Error())
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	f, err := s.root.Open(id)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// The row says the blob exists; the file says otherwise —
			// the vanished-file shape the ErrNotFound doc names. The
			// client sees the same 404 as an unknown id; the operator
			// gets the classified error.
			s.log.Error("mediastore: row without file", "id", id, "err", notFound(id, err).Error())
			http.NotFound(w, r)
			return
		}
		s.log.Error("mediastore: blob open failed", "id", id, "err", err.Error())
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer f.Close()
	// Generated media is immutable, so let the edge cache it forever
	// (PLAN.md §T1, "Generated media is immutable"): the id names
	// exactly these bytes, always.
	w.Header().Set("Content-Type", m.ContentType)
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("ETag", `"`+m.ID+`"`)
	// No modtime: the content is immutable, so conditional requests
	// ride on the ETag alone.
	http.ServeContent(w, r, "", time.Time{}, f)
}

// newID returns a fresh 128-bit random id, hex-encoded — the same
// shape store.NewID produces, generated here so the blob file and the
// metadata row share the name.
func newID() (string, error) {
	return newIDFrom(rand.Reader)
}

// newIDFrom draws 16 bytes of entropy from r and hex-encodes them.
func newIDFrom(r io.Reader) (string, error) {
	var b [idLen / 2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return "", fmt.Errorf("mediastore: new id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// validID reports whether id is a 32-character lowercase hex string —
// the shape newID produces and the only id shape media will look up.
func validID(id string) bool {
	if len(id) != idLen {
		return false
	}
	for _, c := range id {
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return true
}

// normalizeContentType cuts parameters, trims case and whitespace,
// and reports whether what remains is supported.
func normalizeContentType(contentType string) (string, bool) {
	bare, _, _ := strings.Cut(contentType, ";")
	bare = strings.ToLower(strings.TrimSpace(bare))
	return bare, supportedTypes[bare]
}
