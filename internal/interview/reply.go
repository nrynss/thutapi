package interview

import (
	"strings"
	"unicode"
)

// Slot is one entry of the story checklist the interview fills
// (project.md §Pipeline Phase A: hero, companion, want, obstacle,
// turn, ending).
type Slot string

// The six checklist slots, in the order the spec lists them and the
// order filled() reports them.
const (
	// SlotHero is the story's protagonist — who the book is about.
	SlotHero Slot = "hero"
	// SlotCompanion is who goes along — the dragon, the dog, the
	// best friend.
	SlotCompanion Slot = "companion"
	// SlotWant is what the hero wants more than anything.
	SlotWant Slot = "want"
	// SlotObstacle is what stands in the way of the want.
	SlotObstacle Slot = "obstacle"
	// SlotTurn is the moment the problem turns around.
	SlotTurn Slot = "turn"
	// SlotEnding is how it all wraps up.
	SlotEnding Slot = "ending"
)

// slots is every legal Slot; slotSet is its lookup form for parsing.
var slots = [...]Slot{SlotHero, SlotCompanion, SlotWant, SlotObstacle, SlotTurn, SlotEnding}

// parseSlot resolves one control-line token to a Slot. Anything the
// vocabulary does not name is ignored (lenient parse): the model may
// annotate, and an unknown word must never end the interview.
func parseSlot(token string) (Slot, bool) {
	t := Slot(strings.ToLower(strings.TrimSpace(token)))
	for _, s := range slots {
		if t == s {
			return t, true
		}
	}
	return "", false
}

// checklist is the server-side half of the story checklist: the union
// of every slot the model has reported filled so far. Reporting is
// cumulative (see the package doc), so union is idempotent and a
// restart that wiped this map recovers on the model's next report.
type checklist map[Slot]bool

// union marks every named slot filled. Duplicate and unknown names are
// fine — the parser has already dropped unknown ones; duplicates make
// union idempotent.
func (c checklist) union(reported []Slot) {
	for _, s := range reported {
		c[s] = true
	}
}

// full reports whether all six slots are filled — the server-side
// close condition (PLAN.md §T4: close when the checklist is filled).
func (c checklist) full() bool {
	for _, s := range slots {
		if !c[s] {
			return false
		}
	}
	return true
}

// filled returns the filled slots in canonical (spec) order — the
// order event payloads and tests rely on.
func (c checklist) filled() []string {
	out := []string{}
	for _, s := range slots {
		if c[s] {
			out = append(out, string(s))
		}
	}
	return out
}

// reply is one model turn, parsed: the child-visible text with the
// control line stripped, plus whatever the control line carried.
type reply struct {
	// Text is what the child sees: the reply without its control line.
	// It may itself be empty — a turn that is only a control line is
	// degenerate and the loop treats it as a failed turn.
	Text string
	// Slots are the checklist slots this reply reported filled.
	Slots []Slot
	// Chips are the tap options this reply offers, capped at maxChips.
	Chips []string
	// End reports the control line's "end" token: this reply is a
	// goodbye, not a question.
	End bool
}

// parseReply splits a model reply into its child-visible text and its
// control line. Lenient by contract: the control line is recognised
// ONLY as the last non-empty line of the reply, and only when it opens
// with "[[" and closes with "]]"; anything else — no marker, a marker
// with prose after it, an unterminated marker — is treated as plain
// text and changes no state. A malformed marker line that IS the last
// non-empty line is stripped from the text (it was meant as a marker)
// but parsed field-by-field: unknown labels are dropped, unknown slot
// names are dropped, missing fields are empty.
func parseReply(raw string) reply {
	var rep reply
	text, marker, ok := controlLine(raw)
	if !ok {
		return reply{Text: strings.TrimSpace(raw)}
	}
	rep.Text = text
	parseControlLine(marker, &rep)
	return rep
}

// controlLine finds the control line: the last non-empty line of raw
// when it is bracketed. It returns the text above it (trailing blank
// lines stripped) and the bracket interior.
func controlLine(raw string) (text, marker string, ok bool) {
	lines := strings.Split(raw, "\n")
	last := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			last = i
			break
		}
	}
	if last < 0 {
		return "", "", false
	}
	trimmed := strings.TrimSpace(lines[last])
	if !strings.HasPrefix(trimmed, "[[") || !strings.HasSuffix(trimmed, "]]") {
		return "", "", false
	}
	above := strings.TrimRight(strings.Join(lines[:last], "\n"), " \t\r\n")
	return above, trimmed[2 : len(trimmed)-2], true
}

// parseControlLine parses the bracket interior into rep: fields are
// semicolon-separated, each "label: values" (labels case-insensitive)
// except a bare "end" token. Unknown labels and values are ignored.
func parseControlLine(body string, rep *reply) {
	for _, part := range strings.Split(body, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		label, values, hasValues := strings.Cut(part, ":")
		switch strings.ToLower(strings.TrimSpace(label)) {
		case "filled":
			if !hasValues {
				continue
			}
			for _, tok := range strings.Split(values, ",") {
				if s, ok := parseSlot(tok); ok {
					rep.Slots = append(rep.Slots, s)
				}
			}
		case "chips":
			if !hasValues {
				continue
			}
			for _, tok := range strings.Split(values, ",") {
				if c := strings.TrimSpace(tok); c != "" {
					rep.Chips = append(rep.Chips, c)
				}
			}
			if len(rep.Chips) > maxChips {
				rep.Chips = rep.Chips[:maxChips]
			}
		case "end":
			// Bare "end", or "end: yes/true/1" — a model hedging with
			// a value still means end. Anything else after "end:" is
			// ignored (lenient).
			if !hasValues {
				rep.End = true
				continue
			}
			switch strings.ToLower(strings.TrimSpace(values)) {
			case "", "yes", "true", "1":
				rep.End = true
			}
		}
	}
}

// stallWords are the child answers that mean "I'm stuck" (PLAN.md §T4:
// a stall is the model's cue to offer a binary, and the second one in
// a row ends the interview). Matched against the whole normalised
// answer.
var stallWords = map[string]bool{
	"dunno":        true,
	"idk":          true,
	"i dunno":      true,
	"dont know":    true,
	"don't know":   true,
	"i dont know":  true,
	"i don't know": true,
	"no":           true,
	"nope":         true,
	"nah":          true,
	"nothing":      true,
	"none":         true,
	"hmm":          true,
	"hm":           true,
	"um":           true,
	"uh":           true,
	"idc":          true,
	"i dont care":  true,
	"dont care":    true,
	"whatever":     true,
	"nvm":          true,
}

// lowEffort reports whether a child answer is short or stuck — the
// chip signal, not an end. The empty answer, a stall word, or a
// single word after punctuation is stripped all count, so "Mira" and
// "red" raise it exactly like "i dunno" does. The streak it feeds is
// signalled to the model each turn (stallDirective) so the model can
// offer chips; the END fires only on no-progress — two stall answers
// in a row, or a repeated one-word answer — and that decision is
// answer's (http.go), never this predicate's. It knows nothing about
// chips: the caller exempts answers that tapped one of the offered
// chips (a one-word chip answer is a real answer, not a stall).
func lowEffort(answer string) bool {
	var b strings.Builder
	for _, r := range answer {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'':
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(' ') // punctuation separates words, never joins them
		}
	}
	fields := strings.Fields(b.String())
	if len(fields) == 0 {
		return true
	}
	if stallWords[strings.Join(fields, " ")] {
		return true
	}
	return len(fields) == 1
}

// stallAnswer reports whether an answer is one of the stall words —
// a genuine "I'm stuck" rather than a short real answer. Normalised
// the same way the chip match is, so punctuation and case never
// change the verdict.
func stallAnswer(answer string) bool {
	return stallWords[normalizeChip(answer)]
}

// normalizeChip normalises a chip option or an answer for the
// chip-match comparison: lower-cased, punctuation stripped, single
// spaces.
func normalizeChip(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'':
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// matchesChip reports whether the answer is (a normalised form of) one
// of the chips the last question offered — a tapped chip counts as a
// real answer even though it is one word.
func matchesChip(chips []string, answer string) bool {
	a := normalizeChip(answer)
	if a == "" {
		return false
	}
	for _, c := range chips {
		if normalizeChip(c) == a {
			return true
		}
	}
	return false
}
