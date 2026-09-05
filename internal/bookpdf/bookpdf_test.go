package bookpdf

import (
	"bytes"
	"compress/zlib"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"strings"
	"testing"
	"unicode/utf16"
)

// makeTestJPEG generates a small 4:5 JPEG image for testing.
func makeTestJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: 210, G: 112, B: 58, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// makeTestPNG generates a small PNG image for testing.
func makeTestPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// TestRender_HappyPathWithByline tests complete PDF generation with cover and 8 pages.
func TestRender_HappyPathWithByline(t *testing.T) {
	r := NewRenderer()
	jpegBytes := makeTestJPEG(t, 40, 50)

	pages := make([]PageInput, 8)
	for i := 0; i < 8; i++ {
		pages[i] = PageInput{
			N:          i + 1,
			Text:       "This is page " + strings.Repeat("text ", 10),
			ImageBytes: jpegBytes,
		}
	}

	in := Input{
		Title:  "The Wonderful Adventure",
		Byline: "Leo, age 6",
		Pages:  pages,
	}

	pdfBytes, err := r.Render(t.Context(), in)
	if err != nil {
		t.Fatalf("Render failed: %v", err)
	}
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF-")) {
		t.Fatalf("Render output does not start with %%PDF-, got prefix: %q", string(pdfBytes[:min(len(pdfBytes), 10)]))
	}
	if len(pdfBytes) < 1024 {
		t.Fatalf("Render output unexpectedly short: %d bytes", len(pdfBytes))
	}
}

// TestRender_HappyPathWithoutByline tests PDF generation without an author byline.
func TestRender_HappyPathWithoutByline(t *testing.T) {
	r := NewRenderer()
	jpegBytes := makeTestJPEG(t, 20, 25)

	pages := []PageInput{
		{N: 1, Text: "Single page story.", ImageBytes: jpegBytes},
	}

	in := Input{
		Title:  "Solo Journey",
		Byline: "",
		Pages:  pages,
	}

	pdfBytes, err := r.Render(t.Context(), in)
	if err != nil {
		t.Fatalf("Render without byline failed: %v", err)
	}
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF-")) {
		t.Fatalf("Render output does not start with %%PDF-")
	}
}

// TestRender_PNGImageBytes tests that PNG illustrations are handled.
func TestRender_PNGImageBytes(t *testing.T) {
	r := NewRenderer()
	pngBytes := makeTestPNG(t, 20, 25)

	pages := []PageInput{
		{N: 1, Text: "Story with PNG illustration.", ImageBytes: pngBytes},
	}

	in := Input{
		Title:  "PNG Story",
		Byline: "Tester",
		Pages:  pages,
	}

	pdfBytes, err := r.Render(t.Context(), in)
	if err != nil {
		t.Fatalf("Render with PNG failed: %v", err)
	}
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF-")) {
		t.Fatalf("Render output does not start with %%PDF-")
	}
}

// TestRender_InputValidations tests that invalid inputs return ErrInvalidInput.
func TestRender_InputValidations(t *testing.T) {
	r := NewRenderer()
	validImg := makeTestJPEG(t, 20, 25)

	cases := []struct {
		name string
		in   Input
	}{
		{
			name: "empty title",
			in:   Input{Title: "", Pages: []PageInput{{N: 1, Text: "hi", ImageBytes: validImg}}},
		},
		{
			name: "whitespace title",
			in:   Input{Title: "   \t\n  ", Pages: []PageInput{{N: 1, Text: "hi", ImageBytes: validImg}}},
		},
		{
			name: "empty pages",
			in:   Input{Title: "Valid Title", Pages: nil},
		},
		{
			name: "page N is zero",
			in:   Input{Title: "Valid Title", Pages: []PageInput{{N: 0, Text: "hi", ImageBytes: validImg}}},
		},
		{
			name: "page out of order",
			in: Input{
				Title: "Valid Title",
				Pages: []PageInput{
					{N: 1, Text: "page 1", ImageBytes: validImg},
					{N: 3, Text: "page 3", ImageBytes: validImg},
				},
			},
		},
		{
			name: "page with empty image bytes",
			in: Input{
				Title: "Valid Title",
				Pages: []PageInput{
					{N: 1, Text: "page 1", ImageBytes: nil},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := r.Render(t.Context(), tc.in)
			if err == nil {
				t.Fatalf("expected error for case %q, got nil", tc.name)
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("err = %v, want errors.Is(.., ErrInvalidInput)", err)
			}
		})
	}
}

// TestRender_CancelledContextBefore tests that an already cancelled context returns context.Canceled.
func TestRender_CancelledContextBefore(t *testing.T) {
	r := NewRenderer()
	validImg := makeTestJPEG(t, 20, 25)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	in := Input{
		Title: "Cancelled",
		Pages: []PageInput{{N: 1, Text: "hi", ImageBytes: validImg}},
	}

	_, err := r.Render(ctx, in)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want errors.Is(.., context.Canceled)", err)
	}
}

// TestRender_CancelledContextDuring tests cancellation during iteration over pages.
func TestRender_CancelledContextDuring(t *testing.T) {
	r := NewRenderer()
	validImg := makeTestJPEG(t, 20, 25)

	pages := []PageInput{
		{N: 1, Text: "page 1", ImageBytes: validImg},
		{N: 2, Text: "page 2", ImageBytes: validImg},
	}

	in := Input{
		Title: "Cancel During",
		Pages: pages,
	}

	ctxWithCancel, cancelFunc := context.WithCancel(t.Context())
	cancelFunc()
	_, err := r.Render(ctxWithCancel, in)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestRender_RawPDFWireHeader pins the PDF magic byte signature.
func TestRender_RawPDFWireHeader(t *testing.T) {
	r := NewRenderer()
	img := makeTestJPEG(t, 10, 10)
	pdfBytes, err := r.Render(t.Context(), Input{
		Title: "Header Pin",
		Pages: []PageInput{{N: 1, Text: "Hello", ImageBytes: img}},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	wantPrefix := []byte("%PDF-")
	if !bytes.HasPrefix(pdfBytes, wantPrefix) {
		t.Fatalf("PDF bytes = %q, want prefix %q", pdfBytes[:10], wantPrefix)
	}
}

// TestFormatByline verifies that formatByline strips any leading "by " / "By " prefix
// case-insensitively and prepends a clean "By ", asserting that in.Byline = "By Mira"
// or "by Leo" formats cleanly as "By Mira" or "By Leo", not "By By Mira" or "By by Leo".
func TestFormatByline(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		notWant string
	}{
		{name: "with By prefix", input: "By Mira", want: "By Mira", notWant: "By By Mira"},
		{name: "with by prefix", input: "by Leo", want: "By Leo", notWant: "By by Leo"},
		{name: "plain name", input: "Mira", want: "By Mira", notWant: "By By Mira"},
		{name: "case-insensitive BY", input: "BY Leo", want: "By Leo", notWant: "By BY Leo"},
		{name: "padded with whitespace", input: "  by   Maya  ", want: "By Maya"},
		{name: "name starting with By like Byron", input: "Byron", want: "By Byron"},
		{name: "empty string", input: "", want: ""},
		{name: "only whitespace", input: "   ", want: ""},
		{name: "only by prefix", input: "by ", want: ""},
		{name: "only By prefix", input: "By ", want: ""},
		{name: "bare by", input: "by", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := formatByline(tc.input)
			if got != tc.want {
				t.Errorf("formatByline(%q) = %q, want %q", tc.input, got, tc.want)
			}
			if tc.notWant != "" && got == tc.notWant {
				t.Errorf("formatByline(%q) produced duplicate prefix %q", tc.input, tc.notWant)
			}
		})
	}
}

// pdfContentText returns the concatenation of every zlib-compressed
// (FlateDecode) stream's decompressed bytes. fpdf writes the page
// content streams that carry text as FlateDecode streams, so text
// assertions run against their decompressed form.
func pdfContentText(pdfBytes []byte) []byte {
	var out []byte
	for _, chunk := range bytes.Split(pdfBytes, []byte("stream"))[1:] {
		end := bytes.Index(chunk, []byte("endstream"))
		if end < 0 {
			end = len(chunk)
		}
		raw := bytes.Trim(chunk[:end], "\r\n")
		zr, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			continue // not a FlateDecode stream (image data, uncompressed objects, ...)
		}
		dec, derr := io.ReadAll(zr)
		_ = zr.Close() // read finished (or failed); close cannot matter here
		if derr != nil {
			continue
		}
		out = append(out, dec...)
	}
	return out
}

// utf16be encodes s the way fpdf writes UTF-8-font text into content
// streams: two bytes per UTF-16 code unit, big-endian. For Basic Latin
// the visually clean "(By Mira)Tj" is really the byte string
// "(\x00B\x00y\x00 \x00M\x00i\x00r\x00a)Tj".
func utf16be(s string) []byte {
	var out []byte
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u>>8), byte(u&0xff))
	}
	return out
}

// TestRender_BylinePrefixDeduplication pins the byline-dedupe fix at
// the public Render surface, not just the private helper: the cover's
// rendered text must carry exactly one "By " prefix. fpdf writes
// UTF-8-font text as UTF-16BE strings inside FlateDecode-compressed
// content streams, so the pin asserts — over the decompressed streams —
// that the exact expected byline text is present (a call site that
// bypasses formatByline renders the bare name and turns this red) and
// that the doubled prefix "By By" is absent (a naive "By " + name
// helper turns this red).
func TestRender_BylinePrefixDeduplication(t *testing.T) {
	r := NewRenderer()
	img := makeTestJPEG(t, 10, 10)

	cases := []struct {
		name   string
		byline string
		want   string // the exact byline the cover must render
	}{
		{name: "with By prefix", byline: "By Mira", want: "By Mira"},
		{name: "with by prefix", byline: "by Leo", want: "By Leo"},
		{name: "without prefix", byline: "Mira", want: "By Mira"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pdfBytes, err := r.Render(t.Context(), Input{
				Title:  "Deduplication Test",
				Byline: tc.byline,
				Pages:  []PageInput{{N: 1, Text: "Page text", ImageBytes: img}},
			})
			if err != nil {
				t.Fatalf("render failed: %v", err)
			}
			if len(pdfBytes) == 0 {
				t.Fatalf("empty pdf bytes")
			}
			text := pdfContentText(pdfBytes)
			if !bytes.Contains(text, utf16be(tc.want)) {
				t.Fatalf("cover byline %q missing from the rendered text", tc.want)
			}
			if bytes.Contains(text, utf16be("By By")) {
				t.Fatalf("cover text contains the doubled byline prefix %q", "By By")
			}
		})
	}
}
