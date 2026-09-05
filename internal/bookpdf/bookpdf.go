// Package bookpdf renders printable A4 PDF picture books using the
// embedded Fredoka font instances (Regular + Bold) and JPEG
// illustrations.
package bookpdf

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/go-pdf/fpdf"
)

// The embedded Regular (wght 400) and Bold (wght 700) faces are static
// instances of the repo's single vendored Fredoka variable face,
// static/vendor/fonts/Fredoka[wdth,wght].ttf (commit 95cd577, its
// OFL.txt beside it), derived by a recorded fonttools step rather than
// by a second download (t10f-remediation-round2.md, L5):
//
//	fonttools varLib.instancer 'static/vendor/fonts/Fredoka[wdth,wght].ttf' \
//	  wght=400 wdth=100 --update-name-table -o fonts/Fredoka-Regular.ttf
//	fonttools varLib.instancer 'static/vendor/fonts/Fredoka[wdth,wght].ttf' \
//	  wght=700 wdth=100 --update-name-table -o fonts/Fredoka-Bold.ttf
//
// bookpdf cannot embed the vendored file in place: fpdf reads only a
// font's default instance (the variable face defaults to Light, wght
// 300, so one file cannot serve both weights), and //go:embed patterns
// cannot cross the package boundary to static/. Both copies therefore
// live here with the SIL OFL 1.1 licence text (fonts/OFL.txt) beside
// them.
//
//go:embed fonts/Fredoka-Regular.ttf
var fontFredokaRegular []byte

//go:embed fonts/Fredoka-Bold.ttf
var fontFredokaBold []byte

// ErrInvalidInput is returned when Input validation fails: empty title,
// empty pages, non-positive or unordered page numbers, or missing image bytes.
var ErrInvalidInput = errors.New("bookpdf: invalid input")

// PageInput carries the content for one story page.
type PageInput struct {
	// N is the 1-based page number.
	N int
	// Text is the narrative text for the page.
	Text string
	// ImageBytes contains the raw JPEG illustration bytes.
	ImageBytes []byte
}

// Input carries the metadata and story pages for rendering a printable PDF.
type Input struct {
	// Title is the book title on the cover page.
	Title string
	// Byline is the author attribution (e.g. child's name).
	Byline string
	// Pages contains the story pages in sequential order.
	Pages []PageInput
}

// Renderer renders printable PDF books.
type Renderer struct{}

// NewRenderer returns a ready-to-use Renderer.
func NewRenderer() *Renderer {
	return &Renderer{}
}

const (
	// accentR, accentG, accentB is --accent (#d2703a) for the cover title.
	accentR = 210
	accentG = 112
	accentB = 58

	// inkR, inkG, inkB is --ink (#1b1614) for body text and bylines.
	inkR = 27
	inkG = 22
	inkB = 20

	// mutedR, mutedG, mutedB is --muted (#8c827a) for page numbers.
	mutedR = 140
	mutedG = 130
	mutedB = 122
)

// Render generates a printable A4 PDF from the provided book input.
func (r *Renderer) Render(ctx context.Context, in Input) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Title) == "" {
		return nil, fmt.Errorf("%w: title cannot be empty", ErrInvalidInput)
	}
	if len(in.Pages) == 0 {
		return nil, fmt.Errorf("%w: pages cannot be empty", ErrInvalidInput)
	}
	for i, p := range in.Pages {
		if p.N != i+1 {
			return nil, fmt.Errorf("%w: page %d at index %d is out of order (expected %d)", ErrInvalidInput, p.N, i, i+1)
		}
		if len(p.ImageBytes) == 0 {
			return nil, fmt.Errorf("%w: page %d has empty image bytes", ErrInvalidInput, p.N)
		}
	}

	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetAutoPageBreak(false, 0)
	pdf.AddUTF8FontFromBytes("Fredoka", "", fontFredokaRegular)
	pdf.AddUTF8FontFromBytes("Fredoka", "B", fontFredokaBold)

	// Cover page
	pdf.AddPage()
	pdf.SetTextColor(accentR, accentG, accentB)
	pdf.SetFont("Fredoka", "B", 32)
	pdf.SetXY(20, 105)
	pdf.MultiCell(170, 14, in.Title, "", "C", false)

	if byline := formatByline(in.Byline); byline != "" {
		pdf.SetTextColor(inkR, inkG, inkB)
		pdf.SetFont("Fredoka", "", 16)
		pdf.SetXY(20, pdf.GetY()+12)
		pdf.MultiCell(170, 8, byline, "", "C", false)
	}

	// Story pages
	for _, p := range in.Pages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pdf.AddPage()

		imgType := "JPEG"
		if bytes.HasPrefix(p.ImageBytes, []byte("\x89PNG")) {
			imgType = "PNG"
		}
		opt := fpdf.ImageOptions{ImageType: imgType}
		imgName := fmt.Sprintf("page_%d", p.N)
		pdf.RegisterImageOptionsReader(imgName, opt, bytes.NewReader(p.ImageBytes))
		pdf.ImageOptions(imgName, 35, 25, 140, 175, false, opt, 0, "")

		// Text below image
		pdf.SetTextColor(inkR, inkG, inkB)
		pdf.SetFont("Fredoka", "", 15)
		pdf.SetXY(25, 212)
		cleanText := strings.TrimRight(p.Text, "\r\n")
		pdf.MultiCell(160, 8, cleanText, "", "C", false)

		// Page number
		pdf.SetTextColor(mutedR, mutedG, mutedB)
		pdf.SetFont("Fredoka", "", 10)
		pdf.SetXY(25, 280)
		pdf.CellFormat(160, 6, fmt.Sprintf("%d", p.N), "", 0, "C", false, 0, "")
	}

	if err := pdf.Error(); err != nil {
		return nil, fmt.Errorf("bookpdf: fpdf error: %w", err)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("bookpdf: output: %w", err)
	}
	return buf.Bytes(), nil
}

// formatByline normalizes a child's name into a cover byline string (e.g. "By Mira").
// It strips any existing case-insensitive "by " prefix before formatting, preventing
// duplicate prefixes such as "By By Mira" or "By by Leo".
func formatByline(b string) string {
	trimmed := strings.TrimSpace(b)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if lower == "by" {
		return ""
	}
	if strings.HasPrefix(lower, "by ") {
		trimmed = strings.TrimSpace(trimmed[3:])
	}
	if trimmed == "" {
		return ""
	}
	return "By " + trimmed
}
