// Package story is Phase B of the Thutapi pipeline: it turns the
// finished Phase-A interview transcript into the structured picture
// book — title, cast bible, pages — that T6 illustrates, T8 narrates
// and T10 renders (PLAN.md §T5).
//
// The wire shape is exactly the §T5 JSON. Structure runs one Chat
// call over the whole transcript with thinking enabled, extracts the
// JSON from the reply leniently (the model may wrap it in prose or
// code fences), and validates it strictly before returning: a book
// that fails Validate is never returned — it fails loudly into the
// single corrective retry and then to the caller as ErrInvalidStory,
// so no half-rendered book can reach T6/T8/T10.
package story

// Story is the structured picture book Phase B produces: a title, the
// cast bible T6 locks character consistency on, and the pages. The
// JSON tags are the PLAN.md §T5 wire shape; the type is decode-only.
type Story struct {
	Title string       `json:"title"`
	Cast  []CastMember `json:"cast"`
	Pages []Page       `json:"pages"`
}

// CastMember is one entry of the cast bible: who the character is on
// the page (Name), what they look like in every illustration
// (Visual), and how their cloned voice sounds (Voice).
type CastMember struct {
	Name   string `json:"name"`
	Visual string `json:"visual"`
	Voice  Voice  `json:"voice"`
}

// Voice is a cast member's cloned-voice shape: Pitch in semitone
// offsets from the child's recorded clip (project.md §cast: dragon
// −8, mouse +6, narrator 0) and SoundEffects, empty unless the
// character has a signature effect.
type Voice struct {
	Pitch        int    `json:"pitch"`
	SoundEffects string `json:"sound_effects"`
}

// Page is one spread of the book: its number (N), the narration
// (Text), the illustration prompt (Prompt), the cast members on the
// page (Characters), the narration Emotion for T8, and the
// characters' spoken Lines.
type Page struct {
	N          int      `json:"n"`
	Text       string   `json:"text"`
	Prompt     string   `json:"prompt"`
	Characters []string `json:"characters"`
	Emotion    string   `json:"emotion"`
	Lines      []Line   `json:"lines"`
}

// Line is one spoken line on a page: which cast member speaks
// (Character) and what they say (Text).
type Line struct {
	Character string `json:"character"`
	Text      string `json:"text"`
}

// Emotions is the documented set a page's Emotion may carry: GMI's
// emotion vocabulary for minimax-tts-speech-2.8-hd, because T8 passes
// the value straight into the request payload, where an out-of-set
// word is silently ignored. project.md names no set of its own, so
// this is the provider's enum cited from GMI's model-details schema
// (console.gmicloud.ai/api/v1/ie/requestqueue/apikey/models/minimax-tts-speech-2.8-hd,
// t8b-live-record.md) on 2026-09-05 (happy, sad, angry, fearful,
// disgusted, surprised, calm). auto is provider-default and deliberately
// omitted to keep per-page emotion intentional. The system prompt teaches
// it and Validate enforces it from this one slice, so the two cannot drift.
var Emotions = []string{"happy", "sad", "angry", "fearful", "disgusted", "surprised", "calm"}

// PageCount is the number of pages a valid book carries: the system
// prompt teaches it and Validate enforces it from this one constant,
// so the two cannot drift. The two-day cut list may trim the book to
// 6 pages — that cut would change exactly this constant, nothing
// else.
const PageCount = 8

// Turn is story's view of one transcript exchange — who spoke and
// what was said. It is declared here instead of imported from
// internal/store (PLAN.md invariant 3: the consumer declares its own
// narrow seam) so this package stays a pure Phase-B library with no
// store dependency; the Phase-B job field-copies store.Turn into it
// at the call site. Role is opaque — the interviewer/child vocabulary
// is T4's — exactly as the store treats it.
type Turn struct {
	Role string
	Text string
}

// ModelID is the model the Phase-B call runs on. The full MiniMaxAI/
// prefix is load-bearing — the bare id 404s (internal/gmi/text
// quirk 1) — so the raw-wire pin test holds it in place.
const ModelID = "MiniMaxAI/MiniMax-M3"
