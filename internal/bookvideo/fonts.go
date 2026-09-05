package bookvideo

import (
	_ "embed"
)

// The film's drawtext face: a static instance of the repo's single vendored
// Fredoka variable face (static/vendor/fonts/Fredoka[wdth,wght].ttf, commit
// 95cd577, SIL OFL 1.1 licence text beside it). The variable face itself is
// unusable at a chosen weight under drawtext — freetype renders only the
// face's default instance, which is Fredoka Light (wght 300, the fvar
// default; measured visibly thinner in-container) — so, exactly as bookpdf
// does for the PDF, this instance pins Regular 400 so the film's words match
// the book's body text. Derived single-source by the same recorded step
// (t10f-remediation-round2.md L5):
//
//	fonttools varLib.instancer 'static/vendor/fonts/Fredoka[wdth,wght].ttf' \
//	  wght=400 wdth=100 --update-name-table -o fonts/Fredoka-Regular.ttf
//
// The runtime image carries no fonts and no fontconfig config (verified:
// drawtext without fontfile inside the runtime-shaped image fails with
// "Cannot find a valid font for the family Sans"), so bookvideo embeds the
// face and materialises it into the render workdir before ffmpeg runs.
// An explicit Config.FontFile overrides the embedded default.
//
//go:embed fonts/Fredoka-Regular.ttf
var fredokaRegularTTF []byte
