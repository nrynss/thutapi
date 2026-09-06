package audio

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"os/exec"
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
	// persisted keeps what reached the store even after a Delete, so a
	// test can assert the transcode landed BEFORE the clone was tried
	// on a path whose whole point is that the sample is then removed.
	persisted []byte
	deleted   bool
}

func (s *sampleStoreFake) Delete(_ context.Context, id string) error {
	if id != s.id {
		return errors.New("unknown sample")
	}
	s.bytes = nil
	s.deleted = true
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
	s.persisted = b
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
	s.persisted = b
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
	// The default is ffmpeg resolved on PATH — the same default MixBed and
	// the film renderer use — falling back to the container path only when
	// PATH holds no ffmpeg (L1).
	if len(runner.calls) != 1 || runner.calls[0].executable != defaultFFmpeg() {
		t.Fatalf("runner calls = %+v, want default ffmpeg %q", runner.calls, defaultFFmpeg())
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

// TestDecodeVoiceCloneResponse_ReadsOnlyTheOutcome pins the one decision
// this decoder exists to make: the cloned identity comes out of the outcome
// subtree and nothing else. The payload beside it is this package's own
// request echoed back, and its voice_id is the BASE voice the clone was
// seeded from — a decoder that reached for it would narrate every book in
// the library voice while telling the parent it was theirs.
func TestDecodeVoiceCloneResponse_ReadsOnlyTheOutcome(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "outcome voice id",
			raw:  `{"request_id":"req-1","status":"success","payload":{"voice_id":"English_expressive_narrator"},"outcome":{"voice_id":"cloned-voice-abc"}}`,
			want: "cloned-voice-abc",
		},
		{
			name: "surrounding fields ignored",
			raw:  `{"outcome":{"voice_id":"cloned-voice-abc","audio_url":"https://example.com/a.mp3"},"model":"x"}`,
			want: "cloned-voice-abc",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeVoiceCloneResponse([]byte(tc.raw))
			if err != nil || got.VoiceID != tc.want {
				t.Fatalf("DecodeVoiceCloneResponse = %+v, %v; want voice id %q", got, err, tc.want)
			}
		})
	}
}

// TestDecodeVoiceCloneResponse_RejectsEveryOtherShape covers the branches
// that must never produce a voice id, including the echoed payload on its
// own — the exact body that would fool a "first voice_id in the body"
// walker.
func TestDecodeVoiceCloneResponse_RejectsEveryOtherShape(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ``},
		{name: "not json", raw: `not-json`},
		{name: "no outcome", raw: `{"status":"completed"}`},
		{name: "null outcome", raw: `{"outcome":null,"status":"processing"}`},
		{name: "payload echo only", raw: `{"payload":{"voice_id":"English_expressive_narrator"},"outcome":{}}`},
		{name: "outcome is not an object", raw: `{"outcome":"cloned-voice-abc"}`},
		{name: "blank voice id", raw: `{"outcome":{"voice_id":"   "}}`},
		{name: "voice id is not a voice id", raw: `{"outcome":{"voice_id":"../../etc/passwd"}}`},
		{name: "voice id too long", raw: `{"outcome":{"voice_id":"` + strings.Repeat("a", MaxVoiceIDBytes+1) + `"}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DecodeVoiceCloneResponse([]byte(tc.raw))
			if !errors.Is(err, ErrVoiceCloneUnverified) {
				t.Fatalf("DecodeVoiceCloneResponse(%q) = %+v, %v; want ErrVoiceCloneUnverified", tc.raw, got, err)
			}
			if got.VoiceID != "" {
				t.Fatalf("a rejected response returned voice id %q; a non-nil error must mean the rest is meaningless", got.VoiceID)
			}
		})
	}
}

func TestValidVoiceID(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{id: "English_expressive_narrator", want: true},
		{id: "cloned-voice-abc123", want: true},
		{id: "", want: false},
		{id: "has space", want: false},
		{id: "../escape", want: false},
		{id: "quote\"drop", want: false},
		{id: strings.Repeat("a", MaxVoiceIDBytes), want: true},
		{id: strings.Repeat("a", MaxVoiceIDBytes+1), want: false},
	}
	for _, tc := range cases {
		if got := ValidVoiceID(tc.id); got != tc.want {
			t.Errorf("ValidVoiceID(%q) = %v, want %v", tc.id, got, tc.want)
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

// TestVoiceSampleHandler_CloneFailureKeepsTheUploadAndFallsBack is the
// record-first order this route exists in: the recording is transcoded and
// persisted BEFORE the provider is asked for anything, so a provider that
// gives nothing back costs the parent an explanation, not their recording.
// The answer is a success naming the library narrator — never the 502 that
// told a parent their fine recording had failed.
func TestVoiceSampleHandler_CloneFailureKeepsTheUploadAndFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cloner *voiceClonerFake
	}{
		{name: "provider error", cloner: &voiceClonerFake{err: ErrVoiceCloneUnverified}},
		{name: "no voice id", cloner: &voiceClonerFake{empty: true}},
		{name: "base voice echoed back", cloner: &voiceClonerFake{result: VoiceCloneResult{VoiceID: cloneBaseVoiceID}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &sampleStoreFake{id: "id"}
			dir := t.TempDir()
			h, err := NewVoiceSampleHandler(VoiceSampleConfig{Store: store, PublicOrigin: "https://thutapi.nryn.dev", TempDir: dir, ExpiryFile: filepath.Join(dir, "expiry.json"), Runner: &ffmpegFake{output: []byte("ID3")}, UploadToken: "test-upload-token", Cloner: tc.cloner, Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
			if err != nil {
				t.Fatalf("NewVoiceSampleHandler: %v", err)
			}
			t.Cleanup(h.Close)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, sampleRequest(t, []byte("webm"), "audio/webm"))

			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
			}
			// The sample was persisted before the clone was attempted:
			// the clone request names it at its public URL.
			if !strings.HasPrefix(tc.cloner.request.SourceAudio, "https://thutapi.nryn.dev/media/") || tc.cloner.request.VoiceID != cloneBaseVoiceID {
				t.Fatalf("clone request = %+v, want the persisted sample URL seeded from the base voice", tc.cloner.request)
			}
			if string(store.persisted) != "ID3" {
				t.Fatalf("persisted bytes = %q, want the transcoded sample written before the clone call", store.bytes)
			}
			var body VoiceSampleResponse
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response %q: %v", rr.Body.String(), err)
			}
			if body.Narrator != NarratorLibrary {
				t.Fatalf("narrator = %q, want %q", body.Narrator, NarratorLibrary)
			}
			if body.VoiceID != "" || body.MediaURL != "" || body.SourceAudio != "" {
				t.Fatalf("response = %+v, want no voice id and no sample URL on the library path", body)
			}
			// Nothing will fetch the sample now, so it does not linger at
			// a public URL for the rest of its fifteen minutes.
			if !store.deleted {
				t.Fatalf("the unused voice sample was left in the store")
			}
			if h.Tracked("id") {
				t.Fatalf("the deleted voice sample is still tracked for expiry")
			}
		})
	}
}

// TestVoiceSampleHandler_CloneSuccessNamesTheVoice is the other half: a
// provider that DID return a cloned identity answers 201 with the voice the
// book will be read in.
func TestVoiceSampleHandler_CloneSuccessNamesTheVoice(t *testing.T) {
	store := &sampleStoreFake{id: "id"}
	dir := t.TempDir()
	h, err := NewVoiceSampleHandler(VoiceSampleConfig{Store: store, PublicOrigin: "https://thutapi.nryn.dev", TempDir: dir, ExpiryFile: filepath.Join(dir, "expiry.json"), Runner: &ffmpegFake{output: []byte("ID3")}, UploadToken: "test-upload-token", Cloner: &voiceClonerFake{result: VoiceCloneResult{VoiceID: "cloned-voice-abc"}}})
	if err != nil {
		t.Fatalf("NewVoiceSampleHandler: %v", err)
	}
	t.Cleanup(h.Close)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, sampleRequest(t, []byte("webm"), "audio/webm"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rr.Code, rr.Body.String())
	}
	var body VoiceSampleResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response %q: %v", rr.Body.String(), err)
	}
	if body.Narrator != NarratorClone || body.VoiceID != "cloned-voice-abc" {
		t.Fatalf("response = %+v, want the cloned narrator and its voice id", body)
	}
	if !strings.HasPrefix(body.SourceAudio, "https://thutapi.nryn.dev/media/") || body.MediaURL != body.SourceAudio {
		t.Fatalf("response = %+v, want both URLs naming the persisted sample", body)
	}
	// The sample stays for its fifteen minutes here: the provider fetches it.
	if store.deleted {
		t.Fatalf("the sample the provider still needs was deleted")
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
	// The container header alone, with no decode progress line.
	d, err := parseFFmpegDuration([]byte("Duration: 00:00:12.500, start: 0.000, bitrate: 128 kb/s"))
	if err != nil || d != 12500*time.Millisecond {
		t.Fatalf("duration = %v, err=%v, want 12.5s", d, err)
	}
	// Neither a header duration nor a progress time: still an error.
	if _, err := parseFFmpegDuration([]byte("Duration: N/A")); err == nil {
		t.Fatal("parseFFmpegDuration accepted missing duration")
	}

	// H1: a live-muxed WebM reports "Duration: N/A" and the decoded length
	// only on the progress line. The last time= is the full length; the
	// earlier ones are mid-decode, and elapsed= is wall clock, not media.
	live := []byte("  Duration: N/A, start: 0.000000, bitrate: N/A\n" +
		"size=N/A time=00:00:03.42 bitrate=N/A speed= 400x elapsed=0:00:00.00\n" +
		"size=N/A time=00:00:08.00 bitrate=N/A speed= 722x elapsed=0:00:00.01\n")
	d, err = parseFFmpegDuration(live)
	if err != nil || d != 8*time.Second {
		t.Fatalf("live-muxed duration = %v, err=%v, want 8s", d, err)
	}

	// The decoded time wins over the header: it is what will be transcoded,
	// so the MaxVoiceSampleDuration guard is decided on real audio.
	both := []byte("  Duration: 00:00:02.00, start: 0.000000, bitrate: 96 kb/s\n" +
		"size=N/A time=00:01:09.25 bitrate=N/A speed= 900x elapsed=0:00:00.07\n")
	d, err = parseFFmpegDuration(both)
	if err != nil || d != 69250*time.Millisecond {
		t.Fatalf("decoded duration = %v, err=%v, want 69.25s", d, err)
	}

	// elapsed= must never be mistaken for the media time.
	if _, err := parseFFmpegDuration([]byte("elapsed=0:00:00.01\n")); err == nil {
		t.Fatal("parseFFmpegDuration read elapsed= as a media duration")
	}
}

// TestCommandRunnerDuration_RealFFmpeg drives the REAL commandRunner against
// real files, which is what H1 needed: all the handler tests above use
// ffmpegFake, whose Duration returns a canned value, so a probe that could
// not read a browser recording at all passed every one of them.
//
// The WebM here is built the way MediaRecorder builds one — live-muxed, no
// Segment duration — with "-f webm -live 1" to a pipe. That file's container
// header reads "Duration: N/A"; the assertion is that the probe still
// returns its true length. The other formats guard the fallback and the
// formats that already worked.
func TestCommandRunnerDuration_RealFFmpeg(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg not on PATH: %v", err)
	}
	dir := t.TempDir()

	// A live-muxed WebM: written to stdout, so the muxer cannot seek back to
	// fill in the Segment duration — exactly the Chrome/Firefox shape.
	livePath := filepath.Join(dir, "live.webm")
	live, err := os.Create(livePath)
	if err != nil {
		t.Fatalf("create live webm: %v", err)
	}
	cmd := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=8",
		"-c:a", "libopus", "-f", "webm", "-live", "1", "-")
	cmd.Stdout = live
	runErr := cmd.Run()
	closeErr := live.Close()
	if runErr != nil {
		t.Skipf("cannot build a live-muxed webm here: %v", runErr)
	}
	if closeErr != nil {
		t.Fatalf("close live webm: %v", closeErr)
	}

	// Confirm the fixture really has the property H1 is about; without it
	// this test would pass for the wrong reason.
	probe, _ := exec.Command(ffmpeg, "-hide_banner", "-i", livePath, "-f", "null", "-").CombinedOutput()
	if !bytes.Contains(probe, []byte("Duration: N/A")) {
		t.Fatalf("fixture is not live-muxed — ffmpeg reported a container duration:\n%s", probe)
	}

	build := func(name string, args ...string) string {
		t.Helper()
		path := filepath.Join(dir, name)
		full := append([]string{"-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=8", "-y"}, args...)
		out, err := exec.Command(ffmpeg, append(full, path)...).CombinedOutput()
		if err != nil {
			t.Skipf("cannot build %s here: %v: %s", name, err, out)
		}
		return path
	}

	cases := []struct {
		name  string
		input string
	}{
		{"live-muxed webm", livePath},
		{"seekable webm", build("seekable.webm", "-c:a", "libopus")},
		{"m4a", build("clip.m4a", "-c:a", "aac")},
		{"mp3", build("clip.mp3", "-c:a", "libmp3lame")},
		{"wav", build("clip.wav", "-c:a", "pcm_s16le")},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			d, err := commandRunner{}.Duration(context.Background(), ffmpeg, tt.input)
			if err != nil {
				t.Fatalf("Duration(%s) = %v", tt.name, err)
			}
			// Encoder priming and frame granularity move the length by a
			// few tens of milliseconds; 8s ± 0.25s is the real assertion.
			if d < 7750*time.Millisecond || d > 8250*time.Millisecond {
				t.Fatalf("Duration(%s) = %v, want ~8s", tt.name, d)
			}
		})
	}
}

// TestVoiceSampleHandler_BrowserRecordingIsAccepted is H1's end-to-end
// regression test: the whole POST /voice-sample path, with the REAL
// commandRunner and the real ffmpeg, fed the exact kind of file a browser
// produces.
//
// This is the defect's actual entry point. Chrome and Firefox both pick
// audio/webm — the first two candidates static/app.js offers — and
// MediaRecorder mixes WebM live, so the Segment carries no duration and
// ffmpeg prints "Duration: N/A" for it. The duration probe ran before the
// transcode, missed, and every browser recording came back 502 telling the
// adult to choose a different file format. Every other handler test here
// uses ffmpegFake, whose Duration returns a canned value, which is why the
// route could be broken for 100% of Chrome and Firefox users while all
// sixteen of them passed.
func TestVoiceSampleHandler_BrowserRecordingIsAccepted(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skipf("ffmpeg not on PATH: %v", err)
	}

	// Build the recording the way MediaRecorder does: muxed to a pipe, so
	// the muxer can never seek back to write the Segment duration.
	var webm bytes.Buffer
	cmd := exec.Command(ffmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=8",
		"-c:a", "libopus", "-f", "webm", "-live", "1", "-")
	cmd.Stdout = &webm
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot build a live-muxed webm here: %v", err)
	}

	store := &sampleStoreFake{id: "aabbcc"}
	cloner := &voiceClonerFake{}
	h, err := NewVoiceSampleHandler(VoiceSampleConfig{
		Store:        store,
		PublicOrigin: "https://thutapi.nryn.dev",
		TempDir:      t.TempDir(),
		UploadToken:  "test-upload-token",
		Cloner:       cloner,
		// No Runner and no FFmpegPath: the real commandRunner and the
		// resolved default executable, exactly as production wires them.
	})
	if err != nil {
		t.Fatalf("NewVoiceSampleHandler: %v", err)
	}
	t.Cleanup(h.Close)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, sampleRequest(t, webm.Bytes(), "audio/webm; codecs=opus"))
	if rr.Code != http.StatusCreated {
		t.Fatalf("POST a browser recording = %d: %s\n(a 502 here is H1: the duration probe could not read a live-muxed WebM)", rr.Code, rr.Body.String())
	}
	if store.contentType != "audio/mpeg" || len(store.bytes) == 0 {
		t.Errorf("persisted = type %q, %d bytes; want a transcoded audio/mpeg", store.contentType, len(store.bytes))
	}
}

// TestVoiceSampleHandler_AuthIsCheckedBeforeConsent pins L3's reorder: an
// unauthenticated request is refused for its MISSING BEARER, never told
// first that the route wants a consent header. The bearer is the real
// gate; consent is an affirmative declaration by an already-authorized
// caller, not an access control.
func TestVoiceSampleHandler_AuthIsCheckedBeforeConsent(t *testing.T) {
	h := newVoiceSampleTestHandler(t, &sampleStoreFake{id: "id"}, &ffmpegFake{output: []byte("ID3")})

	// Neither header: the answer must be about authorization.
	req := httptest.NewRequest(http.MethodPost, "/voice-sample", bytes.NewReader([]byte("webm")))
	req.Header.Set("Content-Type", "audio/webm")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer, no consent = %d (%s), want 401 — auth is decided first", rr.Code, rr.Body.String())
	}

	// Consent supplied but still no bearer: still 401, and the prober
	// learns nothing about what else the route wants.
	req = httptest.NewRequest(http.MethodPost, "/voice-sample", bytes.NewReader([]byte("webm")))
	req.Header.Set("Content-Type", "audio/webm")
	req.Header.Set("X-Voice-Sample-Consent", "yes")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("consent without a bearer = %d, want 401", rr.Code)
	}

	// Authorized but no consent: NOW the consent requirement is reported.
	req = httptest.NewRequest(http.MethodPost, "/voice-sample", bytes.NewReader([]byte("webm")))
	req.Header.Set("Content-Type", "audio/webm")
	req.Header.Set("Authorization", "Bearer test-upload-token")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("authorized without consent = %d (%s), want 403", rr.Code, rr.Body.String())
	}
}
