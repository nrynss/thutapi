package audio

import (
	"thutapi/internal/gmi/media"
)

// The compile-time half of contract row C1 (t8-round1.md): the real
// media client satisfies the TTS seam this package consumes. The
// declaration builds only while media.SynthesizeSpeech's signature
// matches the seam's exactly — SynthesizeSpeech(ctx, text, emotion,
// voice, model string) — so a drift between the provider and this
// consumer's view of it fails here, at compile time, instead of
// silently at a wiring point. Same shape as the illustrate precedent
// (illustrate/wire_test.go: var _ Imager = (*media.Client)(nil)).
var _ TTS = (*media.Client)(nil)
