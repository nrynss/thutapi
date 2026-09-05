package illustrate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"thutapi/internal/gmi"
	"thutapi/internal/gmi/media"
	"thutapi/internal/gmi/text"
	"thutapi/internal/story"
)

// oneCastStory is the smallest story that exercises the closing loop:
// one drawable character, one page, one locked sheet. Loop tests use
// it so render counts and judge call counts are about one page and
// nothing else.
func oneCastStory() story.Story {
	return story.Story{
		Title: "Mira's Page",
		Cast:  []story.CastMember{{Name: "Mira", Visual: miraVisual}},
		Pages: []story.Page{page(1, "Mira opens the garden gate.", "Mira")},
	}
}

// scriptedImager returns a fakeImager whose renders are byte-distinct
// and deterministic: the nth GenerateImage answers the image bytes
// pngBytes("t7sheet-N") and the nth EditImage answers pngBytes("t7page-N"),
// each wrapped in the live queue-envelope shape (a media_urls URL the
// decode dereferences). Attempt-tagged bytes are what let a test prove
// WHICH render won: the approved page in the Book must equal the last
// attempt's tag, and the judge must have seen that same attempt.
func scriptedImager() *fakeImager {
	f := &fakeImager{}
	var (
		mu       sync.Mutex
		generate int
		edit     int
	)
	f.generate = func(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
		mu.Lock()
		generate++
		tag := fmt.Sprintf("t7sheet-%d", generate)
		mu.Unlock()
		return queueEnvelope(fixtureMedia.url(tag)), nil
	}
	f.edit = func(ctx context.Context, prompt, model string, refImages []string, opts media.ImageOptions) ([]byte, error) {
		mu.Lock()
		edit++
		tag := fmt.Sprintf("t7page-%d", edit)
		mu.Unlock()
		return queueEnvelope(fixtureMedia.url(tag)), nil
	}
	return f
}

// scriptedJudge is a Judge that answers each Chat call with the next
// scripted verdict — the last entry repeats for calls beyond the
// script — or with err when set (the transport-error case). errOn
// picks which call carries the error: errOn == 0 fails every call
// (the first-occurrence transport case); errOn == n fails the n-th
// call and answers the ones before it, which is the mid-loop shape —
// a judge that judged the first render and dies on the
// regeneration's verdict. It records every request it received so a
// test can assert what the judge actually saw.
type scriptedJudge struct {
	mu    sync.Mutex
	out   []bool
	err   error
	errOn int // 1-based call that errors; 0 errors on every call
	seen  int
	reqs  []text.ChatRequest
}

func (j *scriptedJudge) Chat(ctx context.Context, req text.ChatRequest) (*text.ChatResponse, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.reqs = append(j.reqs, req)
	if j.err != nil && (j.errOn == 0 || j.seen+1 == j.errOn) {
		return nil, j.err
	}
	v := j.out[len(j.out)-1] // the last scripted verdict repeats
	if j.seen < len(j.out) {
		v = j.out[j.seen]
	}
	j.seen++
	reply := fmt.Sprintf(`{"match":%t}`, v)
	return &text.ChatResponse{Choices: []text.Choice{{Message: text.AssistantMessage{TextBody: reply}}}}, nil
}

// calls returns how many verdict requests the judge received — the
// transport-error path records its request too, so "not retried" is
// asserted on real call counts.
func (j *scriptedJudge) calls() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.reqs)
}

// requests returns copies of the received requests.
func (j *scriptedJudge) requests() []text.ChatRequest {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]text.ChatRequest(nil), j.reqs...)
}

// garbageJudge answers every verdict request with prose — the judge
// that cannot produce the JSON contract.
type garbageJudge struct{}

func (garbageJudge) Chat(ctx context.Context, req text.ChatRequest) (*text.ChatResponse, error) {
	return &text.ChatResponse{Choices: []text.Choice{{Message: text.AssistantMessage{TextBody: "yes it matches, good page"}}}}, nil
}

// pageBytesInRequest extracts the page image — the LAST image_url
// block of the request's content array — from a verdict request and
// decodes its data: URI back to bytes. The references precede the
// page in the array (project.md §2b's shape), so the page is always
// the final block.
func pageBytesInRequest(t *testing.T, req text.ChatRequest) []byte {
	t.Helper()
	parts, ok := req.Messages[0].Content.([]text.TextPart)
	if !ok || len(parts) < 2 {
		t.Fatalf("request content is not a multi-block array: %#v", req.Messages[0].Content)
	}
	last := parts[len(parts)-1]
	if last.Type != "image_url" || last.ImageURL == nil {
		t.Fatalf("final content block is not an image_url: %#v", last)
	}
	raw, err := base64.StdEncoding.DecodeString(dataURIBody(last.ImageURL.URL))
	if err != nil {
		t.Fatalf("page data: URI does not decode: %v", err)
	}
	return raw
}

// dataURIBody strips "data:<type>;base64," and returns the payload.
func dataURIBody(uri string) string {
	if i := strings.Index(uri, ";base64,"); i >= 0 {
		return uri[i+len(";base64,"):]
	}
	return uri
}

// assertSameRenderArgs asserts every edit call a fakeImager recorded
// carries the same prompt, model and reference URLs — a regeneration
// must be a fair retry of the same draw, not a different one.
func assertSameRenderArgs(t *testing.T, fake *fakeImager, edits int) {
	t.Helper()
	calls, _ := fake.snapshot()
	var editsCalls []imagerCall
	for _, c := range calls {
		if c.kind == "edit" {
			editsCalls = append(editsCalls, c)
		}
	}
	if len(editsCalls) != edits {
		t.Fatalf("edit renders = %d, want %d", len(editsCalls), edits)
	}
	for i := 1; i < len(editsCalls); i++ {
		if editsCalls[i].prompt != editsCalls[0].prompt {
			t.Errorf("regeneration %d changed the prompt:\n got %q\nwant %q", i, editsCalls[i].prompt, editsCalls[0].prompt)
		}
		if editsCalls[i].model != editsCalls[0].model {
			t.Errorf("regeneration %d changed the model: %q vs %q", i, editsCalls[i].model, editsCalls[0].model)
		}
		if len(editsCalls[i].refs) != len(editsCalls[0].refs) {
			t.Fatalf("regeneration %d changed the reference count: %d vs %d", i, len(editsCalls[i].refs), len(editsCalls[0].refs))
		}
		for j := range editsCalls[0].refs {
			if editsCalls[i].refs[j] != editsCalls[0].refs[j] {
				t.Errorf("regeneration %d changed reference %d: %q vs %q", i, j, editsCalls[i].refs[j], editsCalls[0].refs[j])
			}
		}
	}
}

// ------------------------------------------------------------------
// the drift loop: caught, regenerated, capped
// ------------------------------------------------------------------

// TestIllustrate_DriftedPageIsRegenerated pins PLAN.md §T7's Done
// when: a deliberately drifted page — first verdict false — is
// regenerated with the SAME prompt and reference URLs, and the second
// render wins. The judge's second request must carry the second
// render's bytes, not the drifted first attempt's.
func TestIllustrate_DriftedPageIsRegenerated(t *testing.T) {
	fake := scriptedImager()
	judge := &scriptedJudge{out: []bool{false, true}}
	book, err := Illustrate(context.Background(), Config{Imager: fake, Judge: judge}, oneCastStory())
	if err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	if len(book.Pages) != 1 {
		t.Fatalf("Pages = %d, want 1", len(book.Pages))
	}
	// Two renders: the drifted initial and the approved regeneration.
	assertSameRenderArgs(t, fake, 2)
	if got := book.Pages[0].Image; !bytes.Equal(got, pngBytes("t7page-2")) {
		t.Errorf("approved page is the first render's bytes; want the regeneration's (page-2)")
	}
	if got := judge.calls(); got != 2 {
		t.Fatalf("judge calls = %d, want 2 (one per render)", got)
	}
	reqs := judge.requests()
	if !bytes.Equal(pageBytesInRequest(t, reqs[0]), pngBytes("t7page-1")) {
		t.Errorf("first verdict judged the initial render's bytes")
	}
	if !bytes.Equal(pageBytesInRequest(t, reqs[1]), pngBytes("t7page-2")) {
		t.Errorf("second verdict judged the regeneration's bytes, not the drifted first render")
	}
}

// TestIllustrate_SecondRegenerationPasses pins the full retry budget:
// two false verdicts burn one regeneration each, and the third render
// — still the same prompt and sheets — wins.
func TestIllustrate_SecondRegenerationPasses(t *testing.T) {
	fake := scriptedImager()
	judge := &scriptedJudge{out: []bool{false, false, true}}
	book, err := Illustrate(context.Background(), Config{Imager: fake, Judge: judge}, oneCastStory())
	if err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	if len(book.Pages) != 1 {
		t.Fatalf("Pages = %d, want 1", len(book.Pages))
	}
	assertSameRenderArgs(t, fake, 3)
	if got := book.Pages[0].Image; !bytes.Equal(got, pngBytes("t7page-3")) {
		t.Errorf("approved page bytes are not the third render's (page-3)")
	}
	if got := judge.calls(); got != 3 {
		t.Fatalf("judge calls = %d, want 3", got)
	}
	if !bytes.Equal(pageBytesInRequest(t, judge.requests()[2]), pngBytes("t7page-3")) {
		t.Errorf("third verdict judged the third render's bytes")
	}
}

// TestIllustrate_RetryCapExhaustedErrorsTheRun pins the cap itself:
// three false verdicts — the initial render and both regenerations —
// surface ErrConsistency with a zero Book and NO fourth render. A
// stubborn page errors the run, never spins.
func TestIllustrate_RetryCapExhaustedErrorsTheRun(t *testing.T) {
	fake := scriptedImager()
	judge := &scriptedJudge{out: []bool{false, false, false}}
	book, err := Illustrate(context.Background(), Config{Imager: fake, Judge: judge}, oneCastStory())
	if !errors.Is(err, ErrConsistency) {
		t.Fatalf("err = %v, want errors.Is(.., ErrConsistency)", err)
	}
	assertZeroBook(t, book)
	assertSameRenderArgs(t, fake, 3) // exactly the budget; no fourth render
	if got := judge.calls(); got != 3 {
		t.Fatalf("judge calls = %d, want 3", got)
	}
}

// TestIllustrate_EchoPageIsADecodeDefectNotConsistency pins the
// negative guard (PLAN.md §T7 "The blind spot this check does NOT
// cover"): a page byte-identical to its own reference sheet is
// ErrDecodeEcho, the judge is never called, and no regeneration is
// attempted — regenerating would reproduce the echo and burn the cap.
func TestIllustrate_EchoPageIsADecodeDefectNotConsistency(t *testing.T) {
	fake := &fakeImager{}
	// The page render answers with the very sheet bytes the generate
	// phase served — the round-1 H1 mechanism, where the decoder
	// picks the echoed reference instead of the result.
	fake.generate = func(ctx context.Context, prompt, model string, opts media.ImageOptions) ([]byte, error) {
		return queueEnvelope(fixtureMedia.url("echo-sheet")), nil
	}
	fake.edit = func(ctx context.Context, prompt, model string, refImages []string, opts media.ImageOptions) ([]byte, error) {
		return queueEnvelope(fixtureMedia.url("echo-sheet")), nil
	}
	judge := &scriptedJudge{out: []bool{true}} // would "approve" the echo if ever asked
	book, err := Illustrate(context.Background(), Config{Imager: fake, Judge: judge}, oneCastStory())
	if !errors.Is(err, ErrDecodeEcho) {
		t.Fatalf("err = %v, want errors.Is(.., ErrDecodeEcho)", err)
	}
	assertZeroBook(t, book)
	if got := judge.calls(); got != 0 {
		t.Fatalf("judge calls = %d, want 0 — the guard runs first and independently", got)
	}
	calls, _ := fake.snapshot()
	var edits int
	for _, c := range calls {
		if c.kind == "edit" {
			edits++
		}
	}
	if edits != 1 {
		t.Fatalf("edit renders = %d, want exactly 1 — an echo never regenerates", edits)
	}
}

// TestIllustrate_ZeroValueConfigKeepsTheT6Pipeline pins requirement 4:
// a Config with no Judge and no Persist leaves the pipeline byte for
// byte the T6 render loop — the reference sheets and pages render
// exactly once each and nothing extra happens around them.
func TestIllustrate_ZeroValueConfigKeepsTheT6Pipeline(t *testing.T) {
	fake := scriptedImager()
	book, err := Illustrate(context.Background(), Config{Imager: fake}, twoCastStory())
	if err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	calls, _ := fake.snapshot()
	var generate, edit int
	for _, c := range calls {
		switch c.kind {
		case "generate":
			generate++
		case "edit":
			edit++
		}
	}
	if generate != 2 || edit != 2 {
		t.Fatalf("renders = %d generate + %d edit, want 2 + 2 (no judge, no regeneration, no persistence)", generate, edit)
	}
	if len(book.References) != 2 || len(book.Pages) != 2 {
		t.Fatalf("Book = %d refs + %d pages, want 2 + 2", len(book.References), len(book.Pages))
	}
	for i, p := range book.Pages {
		if p.N != i+1 {
			t.Errorf("Pages[%d].N = %d, want %d", i, p.N, i+1)
		}
	}
}

// ------------------------------------------------------------------
// judge failures: transport and malformed replies
// ------------------------------------------------------------------

// TestIllustrate_JudgeTransportErrorSurfacesUnretried pins that a
// judge transport failure — any internal/gmi sentinel, ErrTransient
// included — reaches the caller classified as itself and is NEVER
// retried by this package: one render, one judge call, zero
// regenerations, zero Book. Regeneration is a verdict-driven retry,
// not a transport retry layer.
func TestIllustrate_JudgeTransportErrorSurfacesUnretried(t *testing.T) {
	fake := scriptedImager()
	judge := &scriptedJudge{err: fmt.Errorf("judge upstream: %w", gmi.ErrTransient)}
	book, err := Illustrate(context.Background(), Config{Imager: fake, Judge: judge}, oneCastStory())
	if !errors.Is(err, gmi.ErrTransient) {
		t.Fatalf("err = %v, want errors.Is(.., gmi.ErrTransient)", err)
	}
	if errors.Is(err, ErrConsistency) || errors.Is(err, ErrDecodeEcho) || errors.Is(err, ErrBadVerdict) {
		t.Fatalf("err = %v misclassifies a transport failure as a verdict condition", err)
	}
	assertZeroBook(t, book)
	assertSameRenderArgs(t, fake, 1)
	if got := judge.calls(); got != 1 {
		t.Fatalf("judge calls = %d, want 1 — no second retry layer in this package", got)
	}
}

// TestIllustrate_JudgementGarbageIsABadVerdict pins that a judge
// reply carrying no usable verdict — here prose instead of JSON —
// fails the run with ErrBadVerdict rather than guessing.
func TestIllustrate_JudgementGarbageIsABadVerdict(t *testing.T) {
	fake := scriptedImager()
	book, err := Illustrate(context.Background(), Config{Imager: fake, Judge: garbageJudge{}}, oneCastStory())
	if !errors.Is(err, ErrBadVerdict) {
		t.Fatalf("err = %v, want errors.Is(.., ErrBadVerdict)", err)
	}
	assertZeroBook(t, book)
	assertSameRenderArgs(t, fake, 1)
}

// ------------------------------------------------------------------
// the regeneration path's never-fire error branches (round-1 M1)
// ------------------------------------------------------------------

// TestIllustrate_JudgeErrorMidLoopSurfacesUnretried pins the
// no-second-retry rule where the loop is actually dangerous: the
// judge answers the first render (false — a regeneration fires) and
// dies with a transport error on the regeneration's verdict. The
// retry cap counts FALSE VERDICTS, not errors, so a `continue` here
// would be an unbounded spin — the return path is what bounds the
// loop (round-1 M4: the mutant hung past 8 s). The sentinel after
// exactly two renders and two judge calls pins that the mid-loop
// error is surfaced, never turned into another regeneration.
func TestIllustrate_JudgeErrorMidLoopSurfacesUnretried(t *testing.T) {
	fake := scriptedImager()
	judge := &scriptedJudge{
		out:   []bool{false, true},
		err:   fmt.Errorf("judge upstream: %w", gmi.ErrTransient),
		errOn: 2,
	}
	book, err := Illustrate(context.Background(), Config{Imager: fake, Judge: judge}, oneCastStory())
	if !errors.Is(err, gmi.ErrTransient) {
		t.Fatalf("err = %v, want errors.Is(.., gmi.ErrTransient)", err)
	}
	if errors.Is(err, ErrConsistency) || errors.Is(err, ErrDecodeEcho) || errors.Is(err, ErrBadVerdict) {
		t.Fatalf("err = %v misclassifies a transport failure as a verdict condition", err)
	}
	assertZeroBook(t, book)
	assertSameRenderArgs(t, fake, 2)
	if got := judge.calls(); got != 2 {
		t.Fatalf("judge calls = %d, want 2 (the first verdict answered, the regeneration's verdict errored)", got)
	}
}

// TestIllustrate_RegenerationRenderFailureSurfacesUnretried pins the
// regeneration's EditImage error branch (verify.go's "regenerate: %w"
// return): a false verdict fires a regeneration, the regeneration's
// render call itself fails, and the run surfaces the wrapped error
// with a zero Book and NO third render — a render failure is not a
// false verdict, so it must not burn another retry or spin the loop.
func TestIllustrate_RegenerationRenderFailureSurfacesUnretried(t *testing.T) {
	fake := &fakeImager{}
	var edits int
	fake.edit = func(ctx context.Context, prompt, model string, refImages []string, opts media.ImageOptions) ([]byte, error) {
		edits++
		if edits == 2 {
			return nil, fmt.Errorf("render boom: %w", gmi.ErrTransient)
		}
		return queueEnvelope(fixtureMedia.url(fmt.Sprintf("regen-render-%d", edits))), nil
	}
	judge := &scriptedJudge{out: []bool{false}} // drift the first render
	book, err := Illustrate(context.Background(), Config{Imager: fake, Judge: judge}, oneCastStory())
	if !errors.Is(err, gmi.ErrTransient) {
		t.Fatalf("err = %v, want errors.Is(.., gmi.ErrTransient)", err)
	}
	assertZeroBook(t, book)
	assertSameRenderArgs(t, fake, 2) // the initial render and the failed regeneration; no third
	if got := judge.calls(); got != 1 {
		t.Fatalf("judge calls = %d, want 1 — the failed regeneration is never judged", got)
	}
}

// TestIllustrate_RegenerationDecodeFailureSurfacesUnretried pins the
// regeneration's decode error branch (the second "regenerate: %w"
// return): the regeneration renders but its response carries no
// usable image, and the run surfaces the decode sentinel with a zero
// Book and no third render.
func TestIllustrate_RegenerationDecodeFailureSurfacesUnretried(t *testing.T) {
	fake := &fakeImager{}
	var edits int
	fake.edit = func(ctx context.Context, prompt, model string, refImages []string, opts media.ImageOptions) ([]byte, error) {
		edits++
		if edits == 2 {
			return gifBytes(), nil // decodes to nothing usable
		}
		return queueEnvelope(fixtureMedia.url(fmt.Sprintf("regen-decode-%d", edits))), nil
	}
	judge := &scriptedJudge{out: []bool{false}} // drift the first render
	book, err := Illustrate(context.Background(), Config{Imager: fake, Judge: judge}, oneCastStory())
	if !errors.Is(err, ErrUnsupportedImage) {
		t.Fatalf("err = %v, want errors.Is(.., ErrUnsupportedImage)", err)
	}
	assertZeroBook(t, book)
	assertSameRenderArgs(t, fake, 2) // the initial render and the failed regeneration; no third
	if got := judge.calls(); got != 1 {
		t.Fatalf("judge calls = %d, want 1 — the undecodable regeneration is never judged", got)
	}
}

// emptyChoicesJudge answers every verdict request with a response
// whose choices array is empty — a reply that cannot carry a verdict.
type emptyChoicesJudge struct{}

func (emptyChoicesJudge) Chat(ctx context.Context, req text.ChatRequest) (*text.ChatResponse, error) {
	return &text.ChatResponse{}, nil
}

// TestIllustrate_NoChoicesReplyIsABadVerdict pins judgePage's
// len(resp.Choices) == 0 branch: a judge that answers with no choices
// gave no verdict, so the run fails with ErrBadVerdict and never
// regenerates — an empty reply is not a false verdict.
func TestIllustrate_NoChoicesReplyIsABadVerdict(t *testing.T) {
	fake := scriptedImager()
	book, err := Illustrate(context.Background(), Config{Imager: fake, Judge: emptyChoicesJudge{}}, oneCastStory())
	if !errors.Is(err, ErrBadVerdict) {
		t.Fatalf("err = %v, want errors.Is(.., ErrBadVerdict)", err)
	}
	assertZeroBook(t, book)
	assertSameRenderArgs(t, fake, 1) // no regeneration fires off an unverdictable reply
}

// textlessJudge answers with an assistant message whose content array
// carries no text — an image_url block with no text part — the reply
// shape from which AssistantMessage.Text() can produce nothing.
type textlessJudge struct{}

func (textlessJudge) Chat(ctx context.Context, req text.ChatRequest) (*text.ChatResponse, error) {
	return &text.ChatResponse{Choices: []text.Choice{{
		Message: text.AssistantMessage{Parts: []text.ContentPart{{Type: "image_url"}}},
	}}}, nil
}

// TestIllustrate_TextlessAssistantReplyIsABadVerdict pins judgePage's
// message.Text() error branch: a choice whose message has no text at
// all is ErrBadVerdict via errors.Is, not a guessable verdict.
func TestIllustrate_TextlessAssistantReplyIsABadVerdict(t *testing.T) {
	fake := scriptedImager()
	book, err := Illustrate(context.Background(), Config{Imager: fake, Judge: textlessJudge{}}, oneCastStory())
	if !errors.Is(err, ErrBadVerdict) {
		t.Fatalf("err = %v, want errors.Is(.., ErrBadVerdict)", err)
	}
	assertZeroBook(t, book)
	assertSameRenderArgs(t, fake, 1) // no regeneration fires off an unverdictable reply
}

// ------------------------------------------------------------------
// Done counts pages that are final, not raw renders (round-1 L1)
// ------------------------------------------------------------------

// progressLog records Progress events as they fire. The count is read
// from the judge at verdict time, so reads and the callback share the
// mutex (the callback runs on a render goroutine, the judge on the
// page goroutine).
type progressLog struct {
	mu     sync.Mutex
	events []Progress
}

func (l *progressLog) add(e Progress) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, e)
}

func (l *progressLog) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.events)
}

func (l *progressLog) snapshot() []Progress {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Progress(nil), l.events...)
}

// countingJudge answers scripted verdicts and records, at every Chat
// call, how many Progress events had fired by verdict time. Lock
// order is judge mutex then progressLog mutex; the Progress callback
// takes only the latter, so nothing deadlocks.
type countingJudge struct {
	mu        sync.Mutex
	out       []bool
	seen      int
	log       *progressLog
	atVerdict []int
}

func (j *countingJudge) Chat(ctx context.Context, req text.ChatRequest) (*text.ChatResponse, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	v := j.out[len(j.out)-1] // the last scripted verdict repeats
	if j.seen < len(j.out) {
		v = j.out[j.seen]
	}
	j.seen++
	j.atVerdict = append(j.atVerdict, j.log.len())
	reply := fmt.Sprintf(`{"match":%t}`, v)
	return &text.ChatResponse{Choices: []text.Choice{{Message: text.AssistantMessage{TextBody: reply}}}}, nil
}

// TestIllustrate_DoneCountsFinalPagesNotRawRenders pins the
// renderPages doc's ordering sentence — "The loop is the page's last
// step before its Progress event, so Done counts pages that are
// final, not raw renders" — at the only place it is observable: a
// judge verdict on a page that is still being worked. In a drift run
// the judge's first call (on the not-yet-final initial render) must
// see only the reference sheet's event — Done 1 of 2 — because the
// drifted page's Progress event does not exist until the closing loop
// approves it (round-1 P-B: a report moved above the loop made the
// first verdict see Done 2 of 2 while the page was still drifting).
// The final sequence still ends with the approved page reporting
// Done == Total (2 of 2).
func TestIllustrate_DoneCountsFinalPagesNotRawRenders(t *testing.T) {
	log := &progressLog{}
	judge := &countingJudge{out: []bool{false, true}, log: log}
	book, err := Illustrate(context.Background(), Config{
		Imager:   scriptedImager(),
		Judge:    judge,
		Progress: log.add,
	}, oneCastStory())
	if err != nil {
		t.Fatalf("Illustrate: %v", err)
	}
	if len(book.Pages) != 1 {
		t.Fatalf("Pages = %d, want 1", len(book.Pages))
	}
	judge.mu.Lock()
	at := append([]int(nil), judge.atVerdict...)
	judge.mu.Unlock()
	if len(at) != 2 {
		t.Fatalf("judge calls = %d, want 2 (a drift verdict and the regeneration's)", len(at))
	}
	for i, n := range at {
		if n != 1 {
			t.Errorf("verdict %d saw %d Progress events, want 1 — only the sheet's; a drifted page's event must wait for the closing loop", i+1, n)
		}
	}
	events := log.snapshot()
	if len(events) != 2 {
		t.Fatalf("Progress events = %d, want 2 (the sheet and the approved page)", len(events))
	}
	first := events[0]
	if first.Stage != StageReference || first.Done != 1 || first.Total != 2 {
		t.Errorf("first event = %+v, want the sheet's event with Done 1 of 2", first)
	}
	last := events[len(events)-1]
	if last.Stage != StageIllustration || last.N != 1 || last.Done != 2 || last.Total != 2 {
		t.Errorf("last event = %+v, want the approved page-1 event with Done == Total == 2", last)
	}
}

// TestParseVerdict covers every reply shape parseVerdict can meet:
// the live-verified object, whitespace tolerance, and each malformed
// branch surfaced as ErrBadVerdict.
func TestParseVerdict(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		want  bool
		err   error
	}{
		{name: "match true", reply: `{"match":true}`, want: true},
		{name: "match false with reason", reply: `{"match":false,"reason":"hair is the wrong colour"}`, want: false},
		{name: "surrounding whitespace", reply: "  {\"match\": true} \n", want: true},
		{name: "not JSON", reply: "yes it matches", err: ErrBadVerdict},
		{name: "no match field", reply: `{"reason":"looks fine"}`, err: ErrBadVerdict},
		{name: "match is a string", reply: `{"match":"true"}`, err: ErrBadVerdict},
		{name: "match is null", reply: `{"match":null}`, err: ErrBadVerdict},
		{name: "array not object", reply: `[1,2]`, err: ErrBadVerdict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseVerdict(tc.reply)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("err = %v, want errors.Is(.., %v)", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseVerdict: %v", err)
			}
			if got != tc.want {
				t.Errorf("parseVerdict = %t, want %t", got, tc.want)
			}
		})
	}
}

// ------------------------------------------------------------------
// the wire shape of a verdict request
// ------------------------------------------------------------------

// TestJudgeRequest_RefsAndPageAsDataURIsOnRawWire is the raw-wire pin
// for the quirk T7 builds on (AGENTS.md §Testing rule 4): the
// request's content is a text block plus one image_url block per
// reference and one for the page, images travel as inline data: URIs
// built from the reference bytes (project.md §2b), the page is the
// final block, and the model id is the full MiniMaxAI/MiniMax-M3.
// Asserted against the marshalled bytes so a rename or reorder cannot
// silently unfix it.
func TestJudgeRequest_RefsAndPageAsDataURIsOnRawWire(t *testing.T) {
	refs := []Reference{
		{Name: "Mira", ContentType: "image/png", Image: pngBytes("mira-sheet")},
		{Name: "Bramble", ContentType: "image/jpeg", Image: pngBytes("bramble-sheet")},
	}
	page := pngBytes("page-7")
	req := judgeRequest(DefaultJudgeModel, refs, page, "image/jpeg")
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, `"model":"MiniMaxAI/MiniMax-M3"`) {
		t.Errorf("raw body does not carry the full judge model id:\n%s", body)
	}
	// Re-decode the raw body and walk the content blocks in wire order.
	var wire struct {
		Messages []struct {
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL *struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatalf("raw body does not decode to the content-block shape: %v", err)
	}
	if len(wire.Messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(wire.Messages))
	}
	content := wire.Messages[0].Content
	if len(content) != len(refs)+2 {
		t.Fatalf("content blocks = %d, want text + %d refs + page", len(content), len(refs)+1)
	}
	if content[0].Type != "text" {
		t.Fatalf("first block is %q, want text", content[0].Type)
	}
	for _, want := range []string{"IMAGE 1 (Mira)", "IMAGE 2 (Bramble)", "IMAGE 3 is a newly rendered book page"} {
		if !strings.Contains(content[0].Text, want) {
			t.Errorf("text block missing %q:\n%s", want, content[0].Text)
		}
	}
	for i, ref := range refs {
		blk := content[i+1]
		if blk.Type != "image_url" || blk.ImageURL == nil {
			t.Fatalf("block %d is not an image_url: %#v", i+1, blk)
		}
		want := "data:" + ref.ContentType + ";base64,"
		if !strings.HasPrefix(blk.ImageURL.URL, want) {
			t.Errorf("reference %d URI = %q, want the %q prefix", i, blk.ImageURL.URL, want)
		}
		got, err := base64.StdEncoding.DecodeString(dataURIBody(blk.ImageURL.URL))
		if err != nil || !bytes.Equal(got, ref.Image) {
			t.Errorf("reference %d data: URI does not decode back to the sheet bytes", i)
		}
	}
	last := content[len(content)-1]
	if last.Type != "image_url" || last.ImageURL == nil || !strings.HasPrefix(last.ImageURL.URL, "data:image/jpeg;base64,") {
		t.Fatalf("page block is not the trailing jpeg data: URI: %#v", last)
	}
	got, err := base64.StdEncoding.DecodeString(dataURIBody(last.ImageURL.URL))
	if err != nil || !bytes.Equal(got, page) {
		t.Errorf("page data: URI does not decode back to the page bytes")
	}
}

// TestJudgeRequest_SingleRefNumbering pins the one-sheet instruction,
// the shape every single-character page sends.
func TestJudgeRequest_SingleRefNumbering(t *testing.T) {
	req := judgeRequest(DefaultJudgeModel, []Reference{{Name: "Mira", ContentType: "image/png", Image: pngBytes("mira-sheet")}}, pngBytes("page-1"), "image/png")
	parts := req.Messages[0].Content.([]text.TextPart)
	if len(parts) != 3 {
		t.Fatalf("content blocks = %d, want 3 (text + ref + page)", len(parts))
	}
	text := parts[0].Text
	for _, want := range []string{"IMAGE 1 is the reference sheet for Mira.", "IMAGE 2 is a newly rendered book page.", "Reply with JSON only"} {
		if !strings.Contains(text, want) {
			t.Errorf("text block missing %q:\n%s", want, text)
		}
	}
}
