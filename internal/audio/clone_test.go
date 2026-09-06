package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type sampleStoreFake struct {
	id          string
	err         error
	contentType string
	bytes       []byte
	deleteCh    chan string
	expiryPath  string
	sawExpiry   bool
}

func (s *sampleStoreFake) Delete(_ context.Context, id string) error {
	if id != s.id {
		return errors.New("unknown sample")
	}
	s.bytes = nil
	if s.deleteCh != nil {
		s.deleteCh <- id
	}
	return nil
}

func (s *sampleStoreFake) DeleteIfPresent(ctx context.Context, id string) error {
	if id != s.id && s.bytes == nil {
		return nil
	}
	return s.Delete(ctx, id)
}

type voiceClonerFake struct {
	request VoiceCloneRequest
	err     error
	result  VoiceCloneResult
	empty   bool
}

func (f *voiceClonerFake) CloneVoice(_ context.Context, request VoiceCloneRequest) (VoiceCloneResult, error) {
	f.request = request
	if f.err != nil {
		return VoiceCloneResult{}, f.err
	}
	if f.empty {
		return VoiceCloneResult{}, nil
	}
	if f.result.VoiceID == "" {
		return VoiceCloneResult{VoiceID: "verified-voice"}, nil
	}
	return f.result, nil
}

func (s *sampleStoreFake) Persist(_ context.Context, src io.Reader, contentType string) (string, error) {
	b, readErr := io.ReadAll(src)
	if readErr != nil {
		return "", readErr
	}
	s.contentType = contentType
	s.bytes = b
	if s.err != nil {
		return "", s.err
	}
	return s.id, nil
}

func (s *sampleStoreFake) PersistWithID(_ context.Context, id string, src io.Reader, contentType string) error {
	b, readErr := io.ReadAll(src)
	if readErr != nil {
		return readErr
	}
	s.id = id
	s.contentType = contentType
	s.bytes = b
	if s.expiryPath != "" {
		_, err := os.Stat(s.expiryPath)
		s.sawExpiry = err == nil
	}
	if s.err != nil {
		return s.err
	}
	return nil
}

type ffmpegFake struct {
	err          error
	output       []byte
	calls        []ffmpegCall
	duration     time.Duration
	truncateAtFS bool
}

type ffmpegCall struct {
	executable string
	args       []string
}

func (f *ffmpegFake) Run(_ context.Context, executable string, args ...string) error {
	f.calls = append(f.calls, ffmpegCall{executable: executable, args: append([]string(nil), args...)})
	if f.err != nil {
		return f.err
	}
	output := f.output
	if f.truncateAtFS {
		for i, arg := range args {
			if arg != "-fs" || i+1 >= len(args) {
				continue
			}
			limit, parseErr := strconv.ParseInt(args[i+1], 10, 64)
			if parseErr == nil && int64(len(output)) > limit {
				output = output[:limit]
			}
		}
	}
	return os.WriteFile(args[len(args)-1], output, 0o600)
}

func (f *ffmpegFake) Duration(ctx context.Context, _ string, _ string) (time.Duration, error) {
	if _, ok := ctx.Deadline(); !ok {
		return 0, errors.New("duration probe lacks deadline")
	}
	if f.duration == 0 {
		return 8 * time.Second, nil
	}
	return f.duration, nil
}

func newVoiceSampleTestHandler(t *testing.T, store *sampleStoreFake, runner *ffmpegFake) *VoiceSampleHandler {
	t.Helper()
	h, err := NewVoiceSampleHandler(VoiceSampleConfig{
		Store:        store,
		PublicOrigin: "https://thutapi.nryn.dev",
		FFmpegPath:   "/test/ffmpeg",
		TempDir:      t.TempDir(),
		MaxInput:     128,
		MaxOutput:    64,
		Runner:       runner,
		UploadToken:  "test-upload-token",
		Cloner:       &voiceClonerFake{},
	})
	if err != nil {
		t.Fatalf("NewVoiceSampleHandler: %v", err)
	}
	t.Cleanup(h.Close)
	return h
}

func sampleRequest(t *testing.T, body []byte, contentType string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/voice-sample", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Voice-Sample-Consent", "yes")
	req.Header.Set("Authorization", "Bearer test-upload-token")
	return req
}

func multipartSample(t *testing.T, contentType string, body []byte) *http.Request {
	t.Helper()
	var form bytes.Buffer
	w := multipart.NewWriter(&form)
	part, err := w.CreatePart(textprotoHeader("sample", "voice.webm", contentType))
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close form: %v", err)
	}
	return sampleRequest(t, form.Bytes(), w.FormDataContentType())
}

func textprotoHeader(name, filename, contentType string) textproto.MIMEHeader {
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="`+name+`"; filename="`+filename+`"`)
	h.Set("Content-Type", contentType)
	return h
}

func TestVoiceSampleHandler_DefaultsTranscodeAndPersist(t *testing.T) {
	store := &sampleStoreFake{id: "aabbcc"}
	runner := &ffmpegFake{output: []byte("ID3transcoded")}
	h, err := NewVoiceSampleHandler(VoiceSampleConfig{Store: store, PublicOrigin: "https://thutapi.nryn.dev", TempDir: t.TempDir(), Runner: runner, UploadToken: "test-upload-token", Cloner: &voiceClonerFake{}})
	if err != nil {
		t.Fatalf("NewVoiceSampleHandler defaults: %v", err)
	}
	t.Cleanup(h.Close)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, sampleRequest(t, []byte("webm"), "audio/webm; codecs=opus"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST voice sample = %d: %s", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); !strings.Contains(got, `"media_url":"https://thutapi.nryn.dev/media/`) || !strings.Contains(got, `"source_audio":"https://thutapi.nryn.dev/media/`) {
		t.Errorf("response = %s", got)
	}
	if store.contentType != "audio/mpeg" || string(store.bytes) != "ID3transcoded" {
		t.Errorf("persist = type %q bytes %q, want audio/mpeg transcoded bytes", store.contentType, store.bytes)
	}
	if len(runner.calls) != 1 || runner.calls[0].executable != "/usr/local/bin/ffmpeg" {
		t.Fatalf("runner calls = %+v, want default absolute ffmpeg", runner.calls)
	}
	args := strings.Join(runner.calls[0].args, " ")
	for _, want := range []string{"-nostdin", "-vn", "-c:a libmp3lame", "-b:a 128k", "-ar 44100", "-ac 1"} {
		if !strings.Contains(args, want) {
			t.Errorf("ffmpeg args %q lack %q", args, want)
		}
	}
	if strings.Contains(args, "-fs") {
		t.Errorf("ffmpeg args %q contain truncating -fs", args)
	}
}

func TestVoiceSampleHandler_MultipartAndInputTypes(t *testing.T) {
	for _, tt := range []struct {
		name  string
		type_ string
	}{
		{name: "recorder webm", type_: "audio/webm"},
		{name: "safari mp4", type_: "audio/mp4"},
		{name: "prepared mp3", type_: "audio/mpeg"},
		{name: "prepared wav", type_: "audio/wav"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &sampleStoreFake{id: "id"}
			runner := &ffmpegFake{output: []byte("ID3")}
			h := newVoiceSampleTestHandler(t, store, runner)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, multipartSample(t, tt.type_, []byte("sample")))
			if rr.Code != http.StatusCreated {
				t.Fatalf("multipart %s = %d: %s", tt.type_, rr.Code, rr.Body.String())
			}
			input := runner.calls[0].args[3]
			if filepath.Ext(input) == "" {
				t.Errorf("input path %q has no type-derived extension", input)
			}
		})
	}
}

func TestVoiceSampleHandler_ErrorBranches(t *testing.T) {
	for _, tt := range []struct {
		name       string
		request    func(*testing.T) *http.Request
		runnerErr  error
		runnerOut  []byte
		storeErr   error
		wantStatus int
	}{
		{name: "unsupported type", request: func(t *testing.T) *http.Request { return sampleRequest(t, []byte("no"), "text/plain") }, wantStatus: http.StatusUnsupportedMediaType},
		{name: "empty sample", request: func(t *testing.T) *http.Request { return sampleRequest(t, nil, "audio/webm") }, wantStatus: http.StatusUnsupportedMediaType},
		{name: "missing multipart part", request: func(t *testing.T) *http.Request {
			return sampleRequest(t, []byte("--x--\r\n"), "multipart/form-data; boundary=x")
		}, wantStatus: http.StatusUnsupportedMediaType},
		{name: "input over cap", request: func(t *testing.T) *http.Request {
			return sampleRequest(t, bytes.Repeat([]byte("x"), 129), "audio/webm")
		}, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "ffmpeg failure", request: func(t *testing.T) *http.Request { return sampleRequest(t, []byte("webm"), "audio/webm") }, runnerErr: errors.New("bad codec"), wantStatus: http.StatusBadGateway},
		{name: "empty ffmpeg output", request: func(t *testing.T) *http.Request { return sampleRequest(t, []byte("webm"), "audio/webm") }, wantStatus: http.StatusBadGateway},
		{name: "output over cap", request: func(t *testing.T) *http.Request { return sampleRequest(t, []byte("webm"), "audio/webm") }, runnerOut: bytes.Repeat([]byte("x"), 65), wantStatus: http.StatusRequestEntityTooLarge},
		{name: "persist failure", request: func(t *testing.T) *http.Request { return sampleRequest(t, []byte("webm"), "audio/webm") }, runnerOut: []byte("ID3"), storeErr: errors.New("disk unavailable"), wantStatus: http.StatusInternalServerError},
	} {
		t.Run(tt.name, func(t *testing.T) {
			store := &sampleStoreFake{id: "id", err: tt.storeErr}
			runner := &ffmpegFake{err: tt.runnerErr, output: tt.runnerOut}
			h := newVoiceSampleTestHandler(t, store, runner)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, tt.request(t))
			if rr.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tt.wantStatus, rr.Body.String())
			}
		})
	}
}

func TestVoiceSampleHandler_LongPreparedFileRejectedBeforeTranscode(t *testing.T) {
	store := &sampleStoreFake{id: "id"}
	runner := &ffmpegFake{output: []byte("ID3"), duration: MaxVoiceSampleDuration + time.Millisecond}
	h := newVoiceSampleTestHandler(t, store, runner)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, sampleRequest(t, []byte("compressed long media"), "audio/webm"))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("long prepared file = %d %q, want 413", rr.Code, rr.Body.String())
	}
	if len(runner.calls) != 0 || store.bytes != nil {
		t.Fatalf("long prepared file reached transcode/store: calls=%d bytes=%d", len(runner.calls), len(store.bytes))
	}
}

func TestVoiceSampleHandler_FilesizeFlagMutationCannotSilentlyClip(t *testing.T) {
	store := &sampleStoreFake{id: "id"}
	runner := &ffmpegFake{output: bytes.Repeat([]byte("x"), 65), truncateAtFS: true}
	h := newVoiceSampleTestHandler(t, store, runner)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, sampleRequest(t, []byte("webm"), "audio/webm"))
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("truncating ffmpeg output = %d %q, want 413", rr.Code, rr.Body.String())
	}
	if store.bytes != nil {
		t.Fatalf("truncating ffmpeg output was persisted: %d bytes", len(store.bytes))
	}
}

func TestVoiceSampleHandler_MethodAndConfigErrors(t *testing.T) {
	if _, err := NewVoiceSampleHandler(VoiceSampleConfig{}); !errors.Is(err, ErrNoVoiceSampleStore) {
		t.Fatalf("NewVoiceSampleHandler without store = %v, want ErrNoVoiceSampleStore", err)
	}
	if _, err := NewVoiceSampleHandler(VoiceSampleConfig{Store: &sampleStoreFake{id: "id"}}); !errors.Is(err, ErrNoVoiceSampleOrigin) {
		t.Fatalf("NewVoiceSampleHandler without origin = %v, want ErrNoVoiceSampleOrigin", err)
	}
	for _, origin := range []string{"http://thutapi.example", "https://", "https://thutapi.example/path", "https://user:pass@thutapi.example", "https://localhost", "https://127.0.0.1", "https://[::1]", "https://10.0.0.1", "https://host.internal"} {
		if _, err := NewVoiceSampleHandler(VoiceSampleConfig{Store: &sampleStoreFake{id: "id"}, PublicOrigin: origin}); !errors.Is(err, ErrNoVoiceSampleOrigin) {
			t.Errorf("origin %q accepted with err=%v, want ErrNoVoiceSampleOrigin", origin, err)
		}
	}
	h := newVoiceSampleTestHandler(t, &sampleStoreFake{id: "id"}, &ffmpegFake{output: []byte("ID3")})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/voice-sample", nil))
	if rr.Code != http.StatusMethodNotAllowed || rr.Header().Get("Allow") != http.MethodPost {
		t.Errorf("GET = %d Allow %q, want 405 POST", rr.Code, rr.Header().Get("Allow"))
	}
}

func TestDecodeVoiceCloneResponse_FailsClosed(t *testing.T) {
	for _, raw := range [][]byte{[]byte(`{"outcome":{"voice_id":"provider-voice"}}`), []byte(`{"status":"completed"}`), []byte(`not-json`)} {
		if _, err := DecodeVoiceCloneResponse(raw); !errors.Is(err, ErrVoiceCloneUnverified) {
			t.Errorf("DecodeVoiceCloneResponse(%q) = %v, want ErrVoiceCloneUnverified", raw, err)
		}
	}
}

func TestVoiceSampleHandler_ConsentRequired(t *testing.T) {
	h := newVoiceSampleTestHandler(t, &sampleStoreFake{id: "id"}, &ffmpegFake{output: []byte("ID3")})
	req := sampleRequest(t, []byte("webm"), "audio/webm")
	req.Header.Del("X-Voice-Sample-Consent")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden || !strings.Contains(rr.Body.String(), "consent") {
		t.Fatalf("without consent = %d %q, want 403 consent", rr.Code, rr.Body.String())
	}
}

func TestVoiceSampleHandler_BearerAuthorizationRequired(t *testing.T) {
	cases := []struct {
		name   string
		author string
		want   int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "malformed", author: "Basic test-upload-token", want: http.StatusUnauthorized},
		{name: "wrong", author: "Bearer wrong-upload-token", want: http.StatusUnauthorized},
		{name: "valid", author: "Bearer test-upload-token", want: http.StatusCreated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &sampleStoreFake{id: "id"}
			runner := &ffmpegFake{output: []byte("ID3")}
			h := newVoiceSampleTestHandler(t, store, runner)
			req := sampleRequest(t, []byte("webm"), "audio/webm")
			if tc.author == "" {
				req.Header.Del("Authorization")
			} else {
				req.Header.Set("Authorization", tc.author)
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rr.Code, tc.want, rr.Body.String())
			}
			if tc.want == http.StatusUnauthorized && (len(runner.calls) != 0 || store.bytes != nil) {
				t.Fatalf("unauthorized upload reached runner/store: calls=%d bytes=%d", len(runner.calls), len(store.bytes))
			}
		})
	}
}

func TestVoiceSampleHandler_CloneRequestAndUnverifiedResponse(t *testing.T) {
	store := &sampleStoreFake{id: "id"}
	cloner := &voiceClonerFake{err: ErrVoiceCloneUnverified}
	h, err := NewVoiceSampleHandler(VoiceSampleConfig{Store: store, PublicOrigin: "https://thutapi.nryn.dev", TempDir: t.TempDir(), Runner: &ffmpegFake{output: []byte("ID3")}, UploadToken: "test-upload-token", Cloner: cloner})
	if err != nil {
		t.Fatalf("NewVoiceSampleHandler: %v", err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, sampleRequest(t, []byte("webm"), "audio/webm"))
	if rr.Code != http.StatusBadGateway || !strings.HasPrefix(cloner.request.SourceAudio, "https://thutapi.nryn.dev/media/") || cloner.request.VoiceID != cloneBaseVoiceID {
		t.Fatalf("status=%d request=%+v, want loud failure after confirmed clone request", rr.Code, cloner.request)
	}
	if strings.Contains(rr.Body.String(), `voice_id`) {
		t.Fatalf("response = %s, must not expose an unverified voice id", rr.Body.String())
	}
}

func TestVoiceSampleHandler_ExpiresAndNeverUsesImmutableCache(t *testing.T) {
	now := time.Date(2026, time.September, 6, 0, 0, 0, 0, time.UTC)
	clock := now
	var clockMu sync.Mutex
	store := &sampleStoreFake{id: "id", deleteCh: make(chan string, 1)}
	dir := t.TempDir()
	config := VoiceSampleConfig{Store: store, PublicOrigin: "https://thutapi.nryn.dev", TempDir: dir, ExpiryFile: filepath.Join(dir, "expiry.json"), Runner: &ffmpegFake{output: []byte("ID3")}, UploadToken: "test-upload-token", Cloner: &voiceClonerFake{}, Lifetime: 10 * time.Millisecond, Now: func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return clock
	}}
	h, err := NewVoiceSampleHandler(config)
	if err != nil {
		t.Fatalf("NewVoiceSampleHandler: %v", err)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, sampleRequest(t, []byte("webm"), "audio/webm"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("upload = %d: %s", rr.Code, rr.Body.String())
	}
	mediaReq := httptest.NewRequest(http.MethodGet, "/media/"+store.id, nil)
	mediaReq.SetPathValue("id", store.id)
	served := httptest.NewRecorder()
	if !h.ServeMedia(served, mediaReq, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.WriteHeader(http.StatusOK)
	})) || served.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("active voice sample cache control = %q, want no-store", served.Header().Get("Cache-Control"))
	}
	clockMu.Lock()
	clock = clock.Add(time.Minute)
	clockMu.Unlock()
	select {
	case deletedID := <-store.deleteCh:
		if deletedID != store.id || store.bytes != nil {
			t.Fatalf("automatic expiry deleted %q, bytes=%d; want %q and no bytes", deletedID, len(store.bytes), store.id)
		}
	case <-time.After(time.Second):
		t.Fatal("automatic expiry did not delete sample without a later media GET")
	}
	h.Close()
}

func TestVoiceSampleHandler_ExpiryRecordedBeforePersist(t *testing.T) {
	dir := t.TempDir()
	store := &sampleStoreFake{id: "id", expiryPath: filepath.Join(dir, "expiry.json")}
	h, err := NewVoiceSampleHandler(VoiceSampleConfig{
		Store: store, PublicOrigin: "https://thutapi.nryn.dev", TempDir: dir,
		ExpiryFile: store.expiryPath, Runner: &ffmpegFake{output: []byte("ID3")},
		UploadToken: "test-upload-token", Cloner: &voiceClonerFake{}, Lifetime: time.Minute,
	})
	if err != nil {
		t.Fatalf("NewVoiceSampleHandler: %v", err)
	}
	defer h.Close()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, sampleRequest(t, []byte("webm"), "audio/webm"))
	if rr.Code != http.StatusCreated || !store.sawExpiry {
		t.Fatalf("upload = %d, saw expiry before PersistWithID=%v; expiry must be durable first", rr.Code, store.sawExpiry)
	}
}

func TestVoiceSampleHandler_SentinelsAndMediaStore(t *testing.T) {
	store := &sampleStoreFake{id: "id"}
	runner := &ffmpegFake{err: errors.New("codec unavailable")}
	h := newVoiceSampleTestHandler(t, store, runner)
	if _, _, err := h.receive(sampleRequest(t, []byte("html"), "text/html")); !errors.Is(err, ErrInvalidVoiceSample) {
		t.Fatalf("unsupported receive = %v, want ErrInvalidVoiceSample", err)
	}
	if _, _, err := h.receive(sampleRequest(t, nil, "audio/webm")); !errors.Is(err, ErrNoVoiceSample) {
		t.Fatalf("empty receive = %v, want ErrNoVoiceSample", err)
	}
	input, _, err := h.receive(sampleRequest(t, []byte("webm"), "audio/webm"))
	if err != nil {
		t.Fatalf("receive input: %v", err)
	}
	defer os.Remove(input)
	if _, err := h.transcodeAndPersist(t.Context(), input); !errors.Is(err, ErrVoiceTranscode) {
		t.Fatalf("runner failure = %v, want ErrVoiceTranscode", err)
	}

	real := newNarrationHarness(t, 0)
	persistRunner := &ffmpegFake{output: []byte("ID3saved")}
	persist, err := NewVoiceSampleHandler(VoiceSampleConfig{Store: real.blobs, PublicOrigin: "https://thutapi.nryn.dev", TempDir: t.TempDir(), Runner: persistRunner, UploadToken: "test-upload-token", Cloner: &voiceClonerFake{}})
	if err != nil {
		t.Fatalf("NewVoiceSampleHandler real store: %v", err)
	}
	t.Cleanup(persist.Close)
	rr := httptest.NewRecorder()
	persist.ServeHTTP(rr, sampleRequest(t, []byte("webm"), "audio/webm"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("persist route = %d: %s", rr.Code, rr.Body.String())
	}
	var response struct {
		MediaURL string `json:"media_url"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	id := strings.TrimPrefix(response.MediaURL, "https://thutapi.nryn.dev/media/")
	row, err := real.db.Media(t.Context(), id)
	if err != nil {
		t.Fatalf("persisted media %q: %v", id, err)
	}
	if row.ContentType != "audio/mpeg" || row.SizeBytes != int64(len("ID3saved")) {
		t.Errorf("stored row = %+v, want audio/mpeg with transcoded size", row)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /media/{id}", real.blobs)
	served := httptest.NewRecorder()
	mux.ServeHTTP(served, httptest.NewRequest(http.MethodGet, response.MediaURL, nil))
	if served.Code != http.StatusOK || served.Header().Get("Content-Type") != "audio/mpeg" || served.Body.String() != "ID3saved" {
		t.Errorf("GET %s = %d %q %q, want 200 audio/mpeg transcoded bytes", response.MediaURL, served.Code, served.Header().Get("Content-Type"), served.Body.String())
	}
}

func TestVoiceCloneRequest_TypoPinnedInRawJSON(t *testing.T) {
	raw, err := json.Marshal(VoiceCloneRequest{
		SourceAudio:             "https://thutapi.nryn.dev/media/unguessable",
		Text:                    "A short consented line.",
		VoiceID:                 "operator-verified-id",
		NeedNoiseReduction:      true,
		NeedVolumnNormalization: true,
	})
	if err != nil {
		t.Fatalf("marshal clone request: %v", err)
	}
	for _, want := range []string{`"source_audio"`, `"text"`, `"voice_id"`, `"need_noise_reduction":true`, `"need_volumn_normalization":true`} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Errorf("clone request %s lacks %s", raw, want)
		}
	}
	for _, forbidden := range []string{`"pitch"`, `"timbre"`, `"emotion"`, `"sound_effects"`} {
		if bytes.Contains(raw, []byte(forbidden)) {
			t.Errorf("clone request %s invented clone-only character knob %s", raw, forbidden)
		}
	}
}

func TestNewVoiceCloneRequest_RequiresAbsoluteHTTPSSource(t *testing.T) {
	for _, source := range []string{"/media/id", "http://example.test/media/id", "", "https://", "https://localhost/media/id", "https://127.0.0.1/media/id", "https://[::1]/media/id", "https://10.0.0.1/media/id", "https://host.internal/media/id", "https://host.local/media/id"} {
		if _, err := NewVoiceCloneRequest(source, "say this", "voice"); !errors.Is(err, ErrInvalidVoiceCloneSource) {
			t.Errorf("source %q: err=%v, want ErrInvalidVoiceCloneSource", source, err)
		}
	}
	request, err := NewVoiceCloneRequest("https://thutapi.nryn.dev/media/id", "say this", "voice")
	if err != nil {
		t.Fatalf("valid clone request: %v", err)
	}
	if request.SourceAudio != "https://thutapi.nryn.dev/media/id" || !request.NeedNoiseReduction || !request.NeedVolumnNormalization {
		t.Fatalf("valid clone request = %+v", request)
	}
	if _, err := NewVoiceCloneRequest("https://thutapi.nryn.dev/media/id", "", "voice"); !errors.Is(err, ErrInvalidVoiceCloneRequest) {
		t.Fatalf("missing clone text = %v, want ErrInvalidVoiceCloneRequest", err)
	}
}

func TestParseFFmpegDuration(t *testing.T) {
	d, err := parseFFmpegDuration([]byte("Duration: 00:00:12.500, start: 0.000, bitrate: 128 kb/s"))
	if err != nil || d != 12500*time.Millisecond {
		t.Fatalf("duration = %v, err=%v, want 12.5s", d, err)
	}
	if _, err := parseFFmpegDuration([]byte("Duration: N/A")); err == nil {
		t.Fatal("parseFFmpegDuration accepted missing duration")
	}
}
