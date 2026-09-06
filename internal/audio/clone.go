package audio

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxVoiceSampleBytes caps the compressed recording received from a browser
// or an uploaded file. The eight-to-fifteen second sample should be far below
// this; the cap stops a public endpoint becoming a disk sink.
const MaxVoiceSampleBytes int64 = 5 << 20

// MaxTranscodedVoiceBytes caps the MP3 ffmpeg may write before it reaches the
// media store. A 15-second 128 kbit/s mono clip is about 240 KiB.
const MaxTranscodedVoiceBytes int64 = 2 << 20

// MaxVoiceSampleDuration bounds the decoded source recording. Byte limits
// alone do not stop a long, highly compressed file from being accepted.
const MaxVoiceSampleDuration = 15 * time.Second

const (
	voiceSampleLifetime  = 15 * time.Minute
	voiceDurationTimeout = 5 * time.Second
	cloneSampleText      = "This short recording is a consented voice sample."
	cloneBaseVoiceID     = "English_expressive_narrator"
)

var (
	// ErrNoVoiceSampleStore reports a handler without somewhere to persist its
	// transcoded MP3.
	ErrNoVoiceSampleStore = errors.New("audio: no voice sample store configured")
	// ErrNoVoiceSampleOrigin reports a handler without a public HTTPS origin
	// that GMI can fetch for source_audio.
	ErrNoVoiceSampleOrigin = errors.New("audio: no public voice sample origin configured")
	// ErrNoVoiceSampleToken reports a handler without its required bearer token.
	ErrNoVoiceSampleToken = errors.New("audio: no voice sample upload token configured")
	// ErrNoVoiceCloner reports a handler that cannot send the required provider request.
	ErrNoVoiceCloner = errors.New("audio: no voice cloner configured")
	// ErrNoVoiceSample reports a request with no sample bytes.
	ErrNoVoiceSample = errors.New("audio: no voice sample supplied")
	// ErrInvalidVoiceSample reports an input type ffmpeg is not permitted to
	// receive from this public route.
	ErrInvalidVoiceSample = errors.New("audio: invalid voice sample")
	// ErrVoiceSampleTooLarge reports either input or transcoded output above its
	// fixed cap.
	ErrVoiceSampleTooLarge = errors.New("audio: voice sample too large")
	// ErrVoiceSampleTooLong reports a decoded source recording over the duration
	// cap, regardless of its compressed byte size.
	ErrVoiceSampleTooLong = errors.New("audio: voice sample too long")
	// ErrVoiceSampleConsentRequired reports an upload without affirmative adult
	// consent.
	ErrVoiceSampleConsentRequired = errors.New("audio: voice sample consent required")
	// ErrVoiceSampleUnauthorized reports a request without the configured bearer token.
	ErrVoiceSampleUnauthorized = errors.New("audio: voice sample unauthorized")
	// ErrVoiceTranscode reports ffmpeg failing to produce the required MP3.
	ErrVoiceTranscode = errors.New("audio: voice sample transcode failed")
	// ErrInvalidVoiceCloneSource reports a clone request whose source_audio is
	// not a public HTTPS URL.
	ErrInvalidVoiceCloneSource = errors.New("audio: invalid voice clone source")
	// ErrInvalidVoiceCloneRequest reports a clone request missing a required
	// provider input field.
	ErrInvalidVoiceCloneRequest = errors.New("audio: invalid voice clone request")
	// ErrVoiceCloneUnverified reports a provider response with no recognized voice id.
	ErrVoiceCloneUnverified = errors.New("audio: unverified voice clone response")
)

// VoiceCloneRequest is the confirmed input half of GMI's clone model. The
// provider has not yet been observed returning a clone result, so callers keep
// that response raw until an operator verifies the source_audio-to-voice_id
// handoff against a deployed HTTPS sample URL.
type VoiceCloneRequest struct {
	SourceAudio             string `json:"source_audio"`
	Text                    string `json:"text"`
	VoiceID                 string `json:"voice_id"`
	NeedNoiseReduction      bool   `json:"need_noise_reduction"`
	NeedVolumnNormalization bool   `json:"need_volumn_normalization"`
}

// NewVoiceCloneRequest builds the confirmed request half of the clone
// boundary. It validates the public source URL and requires the provider's
// required text and voice_id fields, while leaving the unverified response and
// clone-to-HD voice-id workflow to the live probe.
func NewVoiceCloneRequest(sourceAudio, text, voiceID string) (VoiceCloneRequest, error) {
	if !isPublicHTTPSURL(sourceAudio) {
		return VoiceCloneRequest{}, fmt.Errorf("%w: source_audio must be an absolute HTTPS URL", ErrInvalidVoiceCloneSource)
	}
	if strings.TrimSpace(text) == "" || strings.TrimSpace(voiceID) == "" {
		return VoiceCloneRequest{}, fmt.Errorf("%w: text and voice_id are required", ErrInvalidVoiceCloneRequest)
	}
	return VoiceCloneRequest{
		SourceAudio:             sourceAudio,
		Text:                    text,
		VoiceID:                 voiceID,
		NeedNoiseReduction:      true,
		NeedVolumnNormalization: true,
	}, nil
}

// VoiceCloneResult is reserved for the verified provider contract. No clone
// response contract has been observed yet, so the live handler never emits a
// result from this type.
type VoiceCloneResult struct {
	VoiceID string
}

// VoiceCloner submits the confirmed clone request and returns a verified voice
// identifier. The consumer owns this narrow interface.
type VoiceCloner interface {
	CloneVoice(ctx context.Context, request VoiceCloneRequest) (VoiceCloneResult, error)
}

// VoiceSampleStore is the consumer-side view of mediastore used by the upload
// handler. It persists only the already-transcoded closed-set audio/mpeg type.
type VoiceSampleStore interface {
	Persist(ctx context.Context, src io.Reader, contentType string) (string, error)
	// PersistWithID writes a blob and metadata row using id supplied by the
	// caller. T13 records the expiry before invoking this method, closing the
	// crash window between persist and the expiry sidecar.
	PersistWithID(ctx context.Context, id string, src io.Reader, contentType string) error
	Delete(ctx context.Context, id string) error
	// DeleteIfPresent is idempotent for a pre-registered id whose persist
	// did not reach the metadata row before a crash.
	DeleteIfPresent(ctx context.Context, id string) error
}

// FFmpegRunner runs the fixed ffmpeg argument list without a shell. Tests use
// it to inspect the executable contract and inject process failures.
type FFmpegRunner interface {
	Run(ctx context.Context, executable string, args ...string) error
	Duration(ctx context.Context, executable, input string) (time.Duration, error)
}

// VoiceSampleConfig configures the one pipe shared by browser recordings and
// file uploads. Its zero-value limits are safe defaults; Store and PublicOrigin
// are required.
type VoiceSampleConfig struct {
	Store        VoiceSampleStore
	PublicOrigin string
	FFmpegPath   string
	TempDir      string
	ExpiryFile   string
	UploadToken  string
	Cloner       VoiceCloner
	CloneVoiceID string
	Lifetime     time.Duration
	Now          func() time.Time
	MaxInput     int64
	MaxOutput    int64
	Runner       FFmpegRunner
}

// VoiceSampleHandler receives a short source recording, transcodes it to a
// mono 44.1 kHz 128 kbit/s MP3, and persists it for /media/{id} serving.
type VoiceSampleHandler struct {
	store        VoiceSampleStore
	publicOrigin string
	ffmpeg       string
	tempDir      string
	maxInput     int64
	maxOutput    int64
	runner       FFmpegRunner
	uploadToken  string
	cloner       VoiceCloner
	cloneVoiceID string
	lifetime     time.Duration
	now          func() time.Time
	expiryFile   string
	mu           sync.Mutex
	expires      map[string]time.Time
	stopSweep    chan struct{}
	sweepDone    chan struct{}
	closeOnce    sync.Once
}

// NewVoiceSampleHandler resolves the safe defaults in VoiceSampleConfig.
func NewVoiceSampleHandler(cfg VoiceSampleConfig) (*VoiceSampleHandler, error) {
	if cfg.Store == nil {
		return nil, fmt.Errorf("audio: new voice sample handler: %w", ErrNoVoiceSampleStore)
	}
	publicOrigin, err := validatePublicOrigin(cfg.PublicOrigin)
	if err != nil {
		return nil, fmt.Errorf("audio: new voice sample handler: %w", err)
	}
	if strings.TrimSpace(cfg.UploadToken) == "" {
		return nil, fmt.Errorf("audio: new voice sample handler: %w", ErrNoVoiceSampleToken)
	}
	if cfg.Cloner == nil {
		return nil, fmt.Errorf("audio: new voice sample handler: %w", ErrNoVoiceCloner)
	}
	ffmpeg := cfg.FFmpegPath
	if ffmpeg == "" {
		ffmpeg = defaultFFmpeg()
	}
	maxInput := cfg.MaxInput
	if maxInput <= 0 {
		maxInput = MaxVoiceSampleBytes
	}
	maxOutput := cfg.MaxOutput
	if maxOutput <= 0 {
		maxOutput = MaxTranscodedVoiceBytes
	}
	runner := cfg.Runner
	if runner == nil {
		runner = commandRunner{}
	}
	lifetime := cfg.Lifetime
	if lifetime <= 0 {
		lifetime = voiceSampleLifetime
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	expiryFile := cfg.ExpiryFile
	if expiryFile == "" {
		expiryFile = filepath.Join(cfg.TempDir, "voice-sample-expiry.json")
	}
	expires, err := loadVoiceSampleExpiries(expiryFile)
	if err != nil {
		return nil, fmt.Errorf("audio: new voice sample handler: load voice sample expiry: %w", err)
	}
	cloneVoiceID := cfg.CloneVoiceID
	if cloneVoiceID == "" {
		cloneVoiceID = cloneBaseVoiceID
	}
	h := &VoiceSampleHandler{store: cfg.Store, publicOrigin: publicOrigin, ffmpeg: ffmpeg, tempDir: cfg.TempDir, maxInput: maxInput, maxOutput: maxOutput, runner: runner, uploadToken: cfg.UploadToken, cloner: cfg.Cloner, cloneVoiceID: cloneVoiceID, lifetime: lifetime, now: now, expiryFile: expiryFile, expires: expires, stopSweep: make(chan struct{}), sweepDone: make(chan struct{})}
	if err := h.sweepExpired(context.Background()); err != nil {
		return nil, fmt.Errorf("audio: new voice sample handler: sweep expired samples: %w", err)
	}
	go h.expiryLoop()
	return h, nil
}

// Close stops the handler's expiry sweeper. Callers should close a handler
// when its server is shutting down so the goroutine has a defined exit path.
func (h *VoiceSampleHandler) Close() {
	h.closeOnce.Do(func() {
		close(h.stopSweep)
	})
	<-h.sweepDone
}

func (h *VoiceSampleHandler) expiryLoop() {
	interval := time.Second
	if h.lifetime > 0 && h.lifetime < interval {
		interval = h.lifetime
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer close(h.sweepDone)
	for {
		select {
		case <-ticker.C:
			// A failure remains in the durable expiry map and is retried on
			// the next tick; the sample is never made ordinary media again.
			_ = h.sweepExpired(context.Background())
		case <-h.stopSweep:
			return
		}
	}
}

// ServeHTTP accepts either a multipart part named sample or a direct audio
// body. Both front doors converge before ffmpeg and mediastore.
func (h *VoiceSampleHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Bearer auth is checked FIRST: it is the real gate, and checking the
	// consent header ahead of it told an unauthenticated prober what the
	// route wants before it had proved anything. Consent is an affirmative
	// declaration by an already-authorized caller, not an access control.
	//
	// TODO(§T11): this route still has no rate limit, so one token holder can
	// drive unbounded 5 MiB transcodes. It needs T11's rate-limiting gate
	// applied to it; nothing here is a substitute for that.
	if !h.hasUploadAuthorization(r) {
		h.writeError(w, ErrVoiceSampleUnauthorized)
		return
	}
	if !hasVoiceSampleConsent(r) {
		h.writeError(w, ErrVoiceSampleConsentRequired)
		return
	}
	// Multipart boundaries and headers are transport overhead; the file itself
	// is still capped in writeInput, while this outer cap bounds that overhead.
	r.Body = http.MaxBytesReader(w, r.Body, h.maxInput+64<<10)
	input, _, err := h.receive(r)
	if err == nil {
		defer os.Remove(input)
		id, transcodeErr := h.transcodeAndPersist(r.Context(), input)
		if transcodeErr != nil {
			err = transcodeErr
		} else {
			mediaURL := h.mediaURL(id)
			request, requestErr := NewVoiceCloneRequest(mediaURL, cloneSampleText, h.cloneVoiceID)
			if requestErr != nil {
				err = requestErr
			} else {
				result, cloneErr := h.cloner.CloneVoice(r.Context(), request)
				if cloneErr != nil {
					err = fmt.Errorf("%w: %w", ErrVoiceCloneUnverified, cloneErr)
				} else if strings.TrimSpace(result.VoiceID) == "" {
					err = ErrVoiceCloneUnverified
				} else {
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Cache-Control", "no-store")
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(VoiceSampleResponse{MediaURL: mediaURL, SourceAudio: mediaURL, VoiceID: result.VoiceID}) // encoding a tiny fixed response cannot fail meaningfully
					return
				}
			}
		}
	}
	h.writeError(w, err)
}

// VoiceSampleResponse is the typed handoff from capture to the confirmed
// clone request boundary. Both fields are absolute public HTTPS URLs; the
// provider response and voice-id handoff remain an explicit live-probe task.
type VoiceSampleResponse struct {
	MediaURL    string `json:"media_url"`
	SourceAudio string `json:"source_audio"`
	VoiceID     string `json:"voice_id"`
}

func hasVoiceSampleConsent(r *http.Request) bool {
	return strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Voice-Sample-Consent")), "yes")
}

func (h *VoiceSampleHandler) hasUploadAuthorization(r *http.Request) bool {
	const prefix = "Bearer "
	provided := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(provided, prefix) {
		return false
	}
	provided = strings.TrimPrefix(provided, prefix)
	if len(provided) != len(h.uploadToken) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(h.uploadToken)) == 1
}

func (h *VoiceSampleHandler) mediaURL(id string) string {
	return h.publicOrigin + "/media/" + url.PathEscape(id)
}

// ServeMedia applies T13's short-lived, no-store policy to a tracked voice
// sample. Every other media id retains the mediastore's immutable behaviour.
// It returns true when it handled the request.
func (h *VoiceSampleHandler) ServeMedia(w http.ResponseWriter, r *http.Request, next http.Handler) bool {
	id := r.PathValue("id")
	h.mu.Lock()
	expiresAt, tracked := h.expires[id]
	h.mu.Unlock()
	if !tracked {
		return false
	}
	if !h.now().Before(expiresAt) {
		if err := h.deleteExpired(r.Context(), id); err != nil {
			http.Error(w, "we could not remove that voice sample", http.StatusInternalServerError)
			return true
		}
		http.NotFound(w, r)
		return true
	}
	next.ServeHTTP(noStoreResponseWriter{ResponseWriter: w}, r)
	return true
}

type noStoreResponseWriter struct {
	http.ResponseWriter
}

func (w noStoreResponseWriter) WriteHeader(status int) {
	w.Header().Set("Cache-Control", "no-store")
	w.ResponseWriter.WriteHeader(status)
}

func (w noStoreResponseWriter) Write(p []byte) (int, error) {
	w.Header().Set("Cache-Control", "no-store")
	return w.ResponseWriter.Write(p)
}

// Tracked reports whether id is a voice sample this handler still owns
// the lifetime of — reserved, live, or expired but not yet swept.
//
// It exists for §T11's retention sweep, which must never delete a row or
// a blob out from under T13's own expiry sidecar: a voice sample is an
// unplaced media row like any other, and the sweep classifies unplaced
// rows by age alone. The sweep asks this before it deletes anything, so
// the 15-minute lifetime the consent copy promises a parent stays this
// handler's to enforce and nobody else's to shorten.
func (h *VoiceSampleHandler) Tracked(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, tracked := h.expires[id]
	return tracked
}

func (h *VoiceSampleHandler) trackSample(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.expires[id] = h.now().Add(h.lifetime).UTC()
	return writeVoiceSampleExpiries(h.expiryFile, h.expires)
}

func (h *VoiceSampleHandler) untrackSample(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.expires, id)
	return writeVoiceSampleExpiries(h.expiryFile, h.expires)
}

func (h *VoiceSampleHandler) sweepExpired(ctx context.Context) error {
	now := h.now()
	h.mu.Lock()
	ids := make([]string, 0, len(h.expires))
	for id, expiresAt := range h.expires {
		if !now.Before(expiresAt) {
			ids = append(ids, id)
		}
	}
	h.mu.Unlock()
	for _, id := range ids {
		if err := h.store.DeleteIfPresent(ctx, id); err != nil {
			return fmt.Errorf("delete expired voice sample %s: %w", id, err)
		}
		h.mu.Lock()
		delete(h.expires, id)
		h.mu.Unlock()
	}
	if len(ids) > 0 {
		h.mu.Lock()
		err := writeVoiceSampleExpiries(h.expiryFile, h.expires)
		h.mu.Unlock()
		if err != nil {
			return fmt.Errorf("persist expired voice sample removal: %w", err)
		}
	}
	return nil
}

func (h *VoiceSampleHandler) deleteExpired(ctx context.Context, id string) error {
	if err := h.store.DeleteIfPresent(ctx, id); err != nil {
		return fmt.Errorf("delete expired voice sample: %w", err)
	}
	return h.untrackSample(id)
}

func loadVoiceSampleExpiries(path string) (map[string]time.Time, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return make(map[string]time.Time), nil
	}
	if err != nil {
		return nil, err
	}
	var expires map[string]time.Time
	if err := json.Unmarshal(raw, &expires); err != nil {
		return nil, err
	}
	if expires == nil {
		return make(map[string]time.Time), nil
	}
	return expires, nil
}

func writeVoiceSampleExpiries(path string, expires map[string]time.Time) error {
	raw, err := json.Marshal(expires)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".voice-sample-expiry-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// DecodeVoiceCloneResponse fails closed until T13b records the provider's
// actual clone response and the clone-to-HD narration handoff. Synthetic JSON
// fields are not evidence of a usable voice identity.
func DecodeVoiceCloneResponse(raw []byte) (VoiceCloneResult, error) {
	if !json.Valid(raw) {
		return VoiceCloneResult{}, fmt.Errorf("%w: provider response is not valid JSON", ErrVoiceCloneUnverified)
	}
	return VoiceCloneResult{}, fmt.Errorf("%w: provider clone response contract is unverified", ErrVoiceCloneUnverified)
}

func (h *VoiceSampleHandler) receive(r *http.Request) (string, string, error) {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return "", "", fmt.Errorf("%w: malformed content type", ErrInvalidVoiceSample)
	}
	if contentType == "multipart/form-data" {
		return h.receiveMultipart(r)
	}
	return h.writeInput(r.Body, contentType)
}

func (h *VoiceSampleHandler) receiveMultipart(r *http.Request) (string, string, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return "", "", fmt.Errorf("%w: malformed multipart upload", ErrInvalidVoiceSample)
	}
	for {
		part, nextErr := reader.NextPart()
		if errors.Is(nextErr, io.EOF) {
			return "", "", ErrNoVoiceSample
		}
		if nextErr != nil {
			return "", "", h.sizeOrInvalid(nextErr)
		}
		if part.FormName() != "sample" {
			part.Close()
			continue
		}
		path, extension, writeErr := h.writeInput(part, part.Header.Get("Content-Type"))
		part.Close()
		return path, extension, writeErr
	}
}

func (h *VoiceSampleHandler) writeInput(src io.Reader, contentType string) (string, string, error) {
	extension, ok := sampleExtension(contentType)
	if !ok {
		return "", "", fmt.Errorf("%w: content type %q", ErrInvalidVoiceSample, contentType)
	}
	f, err := os.CreateTemp(h.tempDir, "thutapi-voice-*"+extension)
	if err != nil {
		return "", "", fmt.Errorf("audio: create voice sample temp file: %w", err)
	}
	path := f.Name()
	n, copyErr := io.Copy(f, io.LimitReader(src, h.maxInput+1))
	closeErr := f.Close()
	if copyErr != nil {
		os.Remove(path)
		return "", "", h.sizeOrInvalid(copyErr)
	}
	if closeErr != nil {
		os.Remove(path)
		return "", "", fmt.Errorf("audio: close voice sample temp file: %w", closeErr)
	}
	if n == 0 {
		os.Remove(path)
		return "", "", ErrNoVoiceSample
	}
	if n > h.maxInput {
		os.Remove(path)
		return "", "", fmt.Errorf("%w: input exceeds %d bytes", ErrVoiceSampleTooLarge, h.maxInput)
	}
	return path, extension, nil
}

func (h *VoiceSampleHandler) transcodeAndPersist(ctx context.Context, input string) (string, error) {
	durationCtx, cancel := context.WithTimeout(ctx, voiceDurationTimeout)
	duration, err := h.runner.Duration(durationCtx, h.ffmpeg, input)
	cancel()
	if err != nil {
		return "", fmt.Errorf("%w: inspect input duration: %w", ErrVoiceTranscode, err)
	}
	if duration > MaxVoiceSampleDuration {
		return "", fmt.Errorf("%w: duration %s exceeds %s", ErrVoiceSampleTooLong, duration, MaxVoiceSampleDuration)
	}
	output, err := os.CreateTemp(h.tempDir, "thutapi-voice-*.mp3")
	if err != nil {
		return "", fmt.Errorf("audio: create transcoded voice temp file: %w", err)
	}
	outputPath := output.Name()
	if err := output.Close(); err != nil {
		os.Remove(outputPath)
		return "", fmt.Errorf("audio: close transcoded voice temp file: %w", err)
	}
	defer os.Remove(outputPath)
	args := []string{"-nostdin", "-y", "-i", input, "-vn", "-c:a", "libmp3lame", "-b:a", "128k", "-ar", "44100", "-ac", "1", outputPath}
	if err := h.runner.Run(ctx, h.ffmpeg, args...); err != nil {
		return "", fmt.Errorf("%w: %w", ErrVoiceTranscode, err)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		return "", fmt.Errorf("%w: output missing: %v", ErrVoiceTranscode, err)
	}
	if info.Size() == 0 {
		return "", fmt.Errorf("%w: output was empty", ErrVoiceTranscode)
	}
	if info.Size() > h.maxOutput {
		return "", fmt.Errorf("%w: output exceeds %d bytes", ErrVoiceSampleTooLarge, h.maxOutput)
	}
	f, err := os.Open(outputPath)
	if err != nil {
		return "", fmt.Errorf("audio: open transcoded voice sample: %w", err)
	}
	defer f.Close()
	id, err := newVoiceSampleID()
	if err != nil {
		return "", fmt.Errorf("audio: create voice sample id: %w", err)
	}
	if err := h.trackSample(id); err != nil {
		return "", fmt.Errorf("audio: track voice sample before persist: %w", err)
	}
	if err := h.store.PersistWithID(ctx, id, f, "audio/mpeg"); err != nil {
		if untrackErr := h.untrackSample(id); untrackErr != nil {
			return "", fmt.Errorf("audio: persist transcoded voice sample: %w (remove expiry: %v)", err, untrackErr)
		}
		return "", fmt.Errorf("audio: persist transcoded voice sample: %w", err)
	}
	return id, nil
}

func newVoiceSampleID() (string, error) {
	var raw [16]byte
	if _, err := io.ReadFull(rand.Reader, raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func (h *VoiceSampleHandler) sizeOrInvalid(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return fmt.Errorf("%w: input exceeds %d bytes", ErrVoiceSampleTooLarge, h.maxInput)
	}
	return fmt.Errorf("%w: malformed upload: %v", ErrInvalidVoiceSample, err)
}

func (h *VoiceSampleHandler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNoVoiceSample), errors.Is(err, ErrInvalidVoiceSample):
		http.Error(w, "choose a short webm, mp4, mp3, or wav sample", http.StatusUnsupportedMediaType)
	case errors.Is(err, ErrVoiceSampleConsentRequired):
		http.Error(w, "adult consent is required for a voice sample", http.StatusForbidden)
	case errors.Is(err, ErrVoiceSampleUnauthorized):
		w.Header().Set("WWW-Authenticate", `Bearer realm="voice-sample"`)
		http.Error(w, "voice sample authorization is required", http.StatusUnauthorized)
	case errors.Is(err, ErrVoiceSampleTooLarge):
		http.Error(w, "voice sample is too large", http.StatusRequestEntityTooLarge)
	case errors.Is(err, ErrVoiceSampleTooLong):
		http.Error(w, "voice sample is too long", http.StatusRequestEntityTooLarge)
	case errors.Is(err, ErrVoiceTranscode):
		http.Error(w, "we could not prepare that voice sample", http.StatusBadGateway)
	case errors.Is(err, ErrVoiceCloneUnverified), errors.Is(err, ErrInvalidVoiceCloneRequest):
		http.Error(w, "we could not verify that voice sample with the voice service", http.StatusBadGateway)
	default:
		http.Error(w, "we could not save that voice sample", http.StatusInternalServerError)
	}
}

func validatePublicOrigin(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !isPublicHost(u.Hostname()) {
		return "", ErrNoVoiceSampleOrigin
	}
	if u.Path != "" && u.Path != "/" {
		return "", ErrNoVoiceSampleOrigin
	}
	return "https://" + u.Host, nil
}

func isPublicHTTPSURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && isPublicHost(u.Hostname())
}

func isPublicHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".example") || strings.HasSuffix(host, ".test") || strings.HasSuffix(host, ".invalid") || !strings.Contains(host, ".") {
		return false
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}
	return true
}

func sampleExtension(contentType string) (string, bool) {
	bare, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(bare) {
	case "audio/webm", "video/webm":
		return ".webm", true
	case "audio/mp4", "video/mp4":
		return ".mp4", true
	case "audio/mpeg":
		return ".mp3", true
	case "audio/wav", "audio/x-wav", "audio/wave":
		return ".wav", true
	default:
		return "", false
	}
}

// containerFFmpeg is where the runtime image puts the static binary
// (Dockerfile: COPY --from=ff /ffmpeg /usr/local/bin/ffmpeg).
const containerFFmpeg = "/usr/local/bin/ffmpeg"

// defaultFFmpeg is the executable the voice transcode runs when
// VoiceSampleConfig.FFmpegPath is empty. It resolves ffmpeg on PATH, the
// same default MixBed (music.go) and the film renderer (internal/bookvideo)
// use, so the transcode is runnable outside the container — on a workstation
// ffmpeg is usually /usr/bin/ffmpeg, and the old hard-coded container path
// made every local upload fail the duration probe. The container path is the
// fallback for an environment with no usable PATH, so the image keeps
// working either way.
func defaultFFmpeg() string {
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		return "ffmpeg"
	}
	return containerFFmpeg
}

type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, executable string, args ...string) error {
	command := exec.CommandContext(ctx, executable, args...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w: %s", filepath.Base(executable), err, strings.TrimSpace(string(output)))
	}
	return nil
}

func (commandRunner) Duration(ctx context.Context, executable, input string) (time.Duration, error) {
	command := exec.CommandContext(ctx, executable, "-hide_banner", "-i", input, "-f", "null", "-")
	output, err := command.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("%s: %w: %s", filepath.Base(executable), err, strings.TrimSpace(string(output)))
	}
	return parseFFmpegDuration(output)
}

// ffmpegDurationPattern matches the container header ffmpeg prints for the
// input: "Duration: 00:00:12.50". A live-muxed stream has no such header
// value and prints "Duration: N/A" instead, which this deliberately misses —
// see ffmpegProgressTimePattern.
var ffmpegDurationPattern = regexp.MustCompile(`Duration:\s*([0-9]+):([0-9]+):([0-9]+(?:\.[0-9]+)?)`)

// ffmpegProgressTimePattern matches the "time=00:00:08.00" field of the
// progress lines the "-f null -" decode emits. This is the DECODED length,
// which a browser recording only has: MediaRecorder mixes WebM live, so its
// Segment carries no duration and the container header reads "Duration: N/A"
// — the shape Chrome and Firefox both produce, because audio/webm is the
// first candidate static/app.js offers. ffmpeg prints progress repeatedly as
// it decodes, so only the LAST occurrence is the full length.
//
// The \b keeps this off ffmpeg's other "…=H:MM:SS" fields (elapsed=, and
// -progress's out_time=, whose underscore is a word character).
var ffmpegProgressTimePattern = regexp.MustCompile(`\btime=\s*([0-9]+):([0-9]+):([0-9]+(?:\.[0-9]+)?)`)

// parseFFmpegDuration reads the input's length out of an "ffmpeg -i <input>
// -f null -" run's stderr. The decoded progress time is preferred over the
// container header: it is the length that will actually be transcoded (so
// the MaxVoiceSampleDuration guard is decided on real audio, not on a
// header's claim), and it is the only one a live-muxed recording reports at
// all. The header is the fallback for output with no progress line.
func parseFFmpegDuration(output []byte) (time.Duration, error) {
	if matches := lastSubmatch(ffmpegProgressTimePattern, output); matches != nil {
		return hmsToDuration(matches)
	}
	matches := ffmpegDurationPattern.FindSubmatch(output)
	if len(matches) != 4 {
		return 0, errors.New("ffmpeg duration was not reported")
	}
	return hmsToDuration(matches)
}

// lastSubmatch returns the submatches of the LAST match of re in b, or nil
// when there is none.
func lastSubmatch(re *regexp.Regexp, b []byte) [][]byte {
	all := re.FindAllSubmatch(b, -1)
	if len(all) == 0 {
		return nil
	}
	return all[len(all)-1]
}

// hmsToDuration converts an (hours, minutes, seconds) submatch triple into a
// duration.
func hmsToDuration(matches [][]byte) (time.Duration, error) {
	hours, err := strconv.ParseInt(string(matches[1]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse ffmpeg hours: %w", err)
	}
	minutes, err := strconv.ParseInt(string(matches[2]), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse ffmpeg minutes: %w", err)
	}
	seconds, err := strconv.ParseFloat(string(matches[3]), 64)
	if err != nil {
		return 0, fmt.Errorf("parse ffmpeg seconds: %w", err)
	}
	return time.Duration(float64(time.Hour)*float64(hours) + float64(time.Minute)*float64(minutes) + seconds*float64(time.Second)), nil
}
