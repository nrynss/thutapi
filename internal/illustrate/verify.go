package illustrate

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"thutapi/internal/gmi/media"
	"thutapi/internal/gmi/text"
)

// DefaultJudgeModel is the model id every consistency verdict is sent
// to: MiniMaxAI/MiniMax-M3, the MiniMax text model with native image
// input, live-verified judging a reference sheet and a render from
// inline base64 (PLAN.md §T7; t6b-live-record.md item 2 — the same
// mechanism this file codifies). The id must carry the full
// "MiniMaxAI/" prefix or the host 404s (AGENTS.md §GMI endpoints;
// internal/gmi/text package doc fact 1). The model is fixed rather
// than a Config knob because it is the only id on the text endpoint
// that reads images; if a successor appears, this constant is the
// one-line switch.
const DefaultJudgeModel = "MiniMaxAI/MiniMax-M3"

// maxPageRegenerations is how many times a page whose verdict says it
// drifted is rendered again before the run gives up. PLAN.md §T7:
// "regenerate on false. Cap at 2 retries so a stubborn page cannot
// spin" — the initial render plus two regenerations, three renders at
// most, and a page that still fails the verdict after the second
// regeneration errors the whole run (the returned Book is zero). One
// regeneration is the safety net for a single bad draw; two bounds a
// genuinely stubborn page without letting it spin.
const maxPageRegenerations = 2

// Judge is the multimodal text client T7's per-page consistency loop
// calls (PLAN.md §T7): one message carrying the reference sheets the
// page was locked to and the page itself, answered with a match
// verdict. It is satisfied by *text.Client; tests substitute a
// scripted fake, so the loop below is exercised without HTTP
// (invariant 3: the consumer declares the interface, one method
// wide).
type Judge interface {
	Chat(ctx context.Context, req text.ChatRequest) (*text.ChatResponse, error)
}

// Sentinel errors the consistency loop declares. Every one is matched
// with errors.Is (PLAN.md invariant 8). The judge's own failures pass
// through as internal/gmi's sentinels, wrapped with which page they
// were for; a judge transport error is never retried here — the text
// client's single internal retry is the only transport retry, and
// regeneration is a verdict-driven retry, not a transport one
// (internal/gmi/errors.go).
var (
	// ErrDecodeEcho reports a page whose bytes are byte-identical to
	// a reference sheet it was locked to — T6 round-1 H1's failure
	// mode, where the decoder picked the request queue's echoed
	// payload image (which is the reference sheet) instead of the
	// real result. It is a decode defect, not a consistency success,
	// so it is its own sentinel and never folds into the regenerate
	// path: regeneration reproduces the echo and would burn the
	// retry cap on every page (PLAN.md §T7, "The blind spot this
	// check does NOT cover").
	ErrDecodeEcho = errors.New("illustrate: page decoded to its own reference sheet")

	// ErrConsistency reports a page that never matched its reference
	// sheets: the initial render and both regenerations were all
	// judged inconsistent. The run fails loudly — a book with a
	// drifted page is not a book — instead of spinning (PLAN.md §T7:
	// cap at 2 retries).
	ErrConsistency = errors.New("illustrate: page still drifted after the regeneration cap")

	// ErrBadVerdict reports a judge reply that carried no usable
	// match verdict: no choices, no text, text that is not JSON, or
	// JSON without a boolean match field. A judge that cannot answer
	// must fail the run loudly rather than be guessed at.
	ErrBadVerdict = errors.New("illustrate: judge returned no match verdict")
)

// judgeRequest builds the chat request that asks the judge whether
// page matches the reference sheets in refs. The message is one
// content array (project.md §2b's verified shape): a text block
// numbering the images, one image_url block per reference sheet as an
// inline data: URI, and the page itself as the final image_url block.
// The page travels last so the instruction's numbering is stable no
// matter how many sheets a page is locked to.
func judgeRequest(model string, refs []Reference, page []byte, pageCT string) text.ChatRequest {
	content := make([]text.TextPart, 0, len(refs)+2)
	content = append(content, text.TextPart{Type: "text", Text: judgeInstruction(refs)})
	for _, ref := range refs {
		content = append(content, imagePart(dataURI(ref.ContentType, ref.Image)))
	}
	content = append(content, imagePart(dataURI(pageCT, page)))
	return text.ChatRequest{
		Model: model,
		Messages: []text.Message{{
			Role:    "user",
			Content: content,
		}},
	}
}

// judgeInstruction numbers the images the verdict request carries so
// the judge knows which are references and which is the new page.
func judgeInstruction(refs []Reference) string {
	b := &strings.Builder{}
	b.WriteString("Judge character consistency for a children's picture book. ")
	if len(refs) == 1 {
		fmt.Fprintf(b, "IMAGE 1 is the reference sheet for %s. IMAGE 2 is a newly rendered book page. ", refs[0].Name)
	} else {
		named := make([]string, len(refs))
		for i, ref := range refs {
			named[i] = fmt.Sprintf("IMAGE %d (%s)", i+1, ref.Name)
		}
		fmt.Fprintf(b, "The first %d images are reference sheets, in this order: %s. IMAGE %d is a newly rendered book page. ",
			len(refs), strings.Join(named, ", "), len(refs)+1)
	}
	b.WriteString("Decide whether every character in the page matches the reference sheet for that same character — same identity, hairstyle, fur or markings, colours and proportions. ")
	b.WriteString("Report match=false when any character the page should show is missing, visibly changed, or swapped for another. ")
	b.WriteString(`Reply with JSON only, no prose and no markdown: {"match": true, "reason": "<one short sentence>"}`)
	return b.String()
}

func imagePart(uri string) text.TextPart {
	return text.TextPart{Type: "image_url", ImageURL: &text.ImageURLRef{URL: uri}}
}

// dataURI inlines image bytes the way the text endpoint wants them
// (project.md §2b: images go in as inline base64 — no hosting needed;
// the generation endpoint's URL-array contract is the media client's,
// not the judge's). contentType is one of the closed image set this
// package decodes, so the data: URI is always well-formed.
func dataURI(contentType string, b []byte) string {
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(b)
}

// parseVerdict decodes the judge's reply: a JSON object with a
// boolean match field (the shape M3 returned live — see PLAN.md §T7
// and project.md §2b). Anything else is ErrBadVerdict: a judge that
// answers with prose or an empty object gave no verdict, and guessing
// one would be how a drifted page ships.
func parseVerdict(reply string) (bool, error) {
	var v struct {
		Match *bool `json:"match"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(reply)), &v); err != nil {
		return false, fmt.Errorf("%w: judge reply is not JSON: %s", ErrBadVerdict, excerpt([]byte(reply)))
	}
	if v.Match == nil {
		return false, fmt.Errorf("%w: judge reply carries no match field: %s", ErrBadVerdict, excerpt([]byte(reply)))
	}
	return *v.Match, nil
}

// echoOfReference reports whether page is byte-identical to one of
// the reference sheets it was locked to. This is the exact-echo case
// the decode defect produces (the echoed payload decodes to the very
// reference bytes; t6b-live-record.md item 2 asserted the same
// byte-identity check live), so the guard is a plain byte comparison
// and needs no model call. It runs before any verdict is asked and on
// every render of the page, regenerated ones included.
func echoOfReference(page []byte, locked []Reference) (Reference, bool) {
	for _, ref := range locked {
		if bytes.Equal(page, ref.Image) {
			return ref, true
		}
	}
	return Reference{}, false
}

// refURLs returns the reference sheet URLs of locked, in order — what
// a regeneration's EditImage call sends as payload.image. Keeping the
// prompt and this array identical across attempts is what makes a
// regeneration a fair retry of the same draw.
func refURLs(locked []Reference) []string {
	urls := make([]string, len(locked))
	for i, ref := range locked {
		urls[i] = ref.URL
	}
	return urls
}

// refImages returns the reference sheet bytes of locked, in order —
// what the decode of a regeneration must exclude as the request's own
// echo (the i2i record echoes payload.image back).
func refImages(locked []Reference) [][]byte {
	images := make([][]byte, len(locked))
	for i, ref := range locked {
		images[i] = ref.Image
	}
	return images
}

// closePage is T7's closing loop for one rendered page (PLAN.md §T7).
// The page has already been rendered and decoded by renderPages; this
// decides whether it is final and, when Config.Persist is set, writes
// it.
//
// The loop, per page:
//
//  1. The echo guard runs first, independently of any verdict: a page
//     byte-identical to a sheet it was locked to is ErrDecodeEcho (a
//     decode defect), never a consistency success, and never a
//     regeneration — regenerating would reproduce the echo and burn
//     the cap.
//  2. With a Judge configured, the reference sheets and the page go
//     to M3 in one message; a match verdict makes the page final. A
//     false verdict regenerates the page — the SAME prompt and the
//     SAME sheet URLs — up to maxPageRegenerations times; a page
//     still drifting after the second regeneration is ErrConsistency
//     and fails the run. A judge error (any internal/gmi sentinel,
//     including ErrTransient) also fails the page: it is surfaced,
//     never retried here.
//  3. With no Judge, a render that decodes is the approval — a caller
//     can persist without spending a model call.
//
// Only a page that reaches approval is persisted, and only after the
// verdict: a page becomes final when this loop approves it, so the
// store sees one write per page and regeneration never churns the
// one-illustration-per-page slot (PLAN.md §T7, "Persistence lands
// here").
func (r *renderer) closePage(ctx context.Context, page *Illustration, locked []Reference) error {
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			raw, err := r.imager.EditImage(ctx, page.Prompt, r.model, refURLs(locked), media.ImageOptions{})
			if err != nil {
				return fmt.Errorf("regenerate: %w", err)
			}
			img, ct, _, err := r.decodeImage(ctx, raw, sentImages{bytes: refImages(locked), urls: refURLs(locked)})
			if err != nil {
				return fmt.Errorf("regenerate: %w", err)
			}
			page.Image = img
			page.ContentType = ct
		}
		if ref, echo := echoOfReference(page.Image, locked); echo {
			return fmt.Errorf("%w: page decoded byte-identical to the reference sheet for %q (t6-round1.md H1)", ErrDecodeEcho, ref.Name)
		}
		if r.judge == nil {
			break
		}
		ok, err := r.judgePage(ctx, page, locked)
		if err != nil {
			return err
		}
		if ok {
			break
		}
		if attempt >= maxPageRegenerations {
			return fmt.Errorf("%w: the initial render and %d regenerations were all judged inconsistent", ErrConsistency, maxPageRegenerations)
		}
	}
	if r.persist != nil {
		return r.persist.storePage(ctx, page)
	}
	return nil
}

// judgePage sends one page's verdict request to the configured judge
// and decodes the match answer. The judge's own errors — every
// internal/gmi sentinel, including ErrTransient — are wrapped and
// surfaced as they are: this package adds no retry layer of its own,
// so a transport failure costs the page (and the run) rather than
// burning the regeneration cap.
func (r *renderer) judgePage(ctx context.Context, page *Illustration, locked []Reference) (bool, error) {
	resp, err := r.judge.Chat(ctx, judgeRequest(DefaultJudgeModel, locked, page.Image, page.ContentType))
	if err != nil {
		return false, fmt.Errorf("consistency judge: %w", err)
	}
	if len(resp.Choices) == 0 {
		return false, fmt.Errorf("%w: the judge returned no choices", ErrBadVerdict)
	}
	reply, err := resp.Choices[0].Message.Text()
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrBadVerdict, err)
	}
	return parseVerdict(reply)
}
