// Package pdf renders plain text as a PDF file, with an embedded CJK font
// so Japanese report content (feedback messages, transcript excerpts --
// the majority of this system's actual text) renders correctly instead of
// being dropped. This replaces the earlier base-14-Helvetica-only, hand
// rolled PDF writer (plan/post-session-report-implementation.md Step 7),
// which could only emit plain ASCII and silently discarded everything
// else.
package pdf

import (
	_ "embed"
	"fmt"

	"github.com/signintech/gopdf"
)

// notoSansJPFont is Noto Sans JP Regular (SIL Open Font License 1.1, see
// fonts/OFL.txt), embedded at build time so the binary/container image is
// self-contained -- no font file needs to exist on the Cloud Run instance's
// filesystem. gopdf subsets it per document (fonts/subset_font_obj.go), so
// only glyphs actually used end up in each rendered PDF; the ~9MB source
// font only adds to the Go binary, not to every report.pdf.
//
//go:embed fonts/NotoSansJP-Regular.ttf
var notoSansJPFont []byte

const (
	fontFamily   = "NotoSansJP"
	fontSize     = 11.0
	lineHeight   = 16.0
	marginLeft   = 40.0
	marginTop    = 40.0
	marginRight  = 40.0
	marginBottom = 40.0
)

// Render builds a multi-page PDF with title as the first line of the first
// page followed by lines. Each line is word-wrapped (see wrapLine) to fit
// the page width and pagination happens automatically once a page's
// content area is full.
func Render(title string, lines []string) ([]byte, error) {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})

	if err := pdf.AddTTFFontData(fontFamily, notoSansJPFont); err != nil {
		return nil, fmt.Errorf("pdf: load embedded font: %w", err)
	}
	if err := pdf.SetFont(fontFamily, "", fontSize); err != nil {
		return nil, fmt.Errorf("pdf: set font: %w", err)
	}

	contentWidth := gopdf.PageSizeA4.W - marginLeft - marginRight
	pageBottom := gopdf.PageSizeA4.H - marginBottom
	pdf.AddPage()
	y := marginTop

	allLines := append([]string{title, ""}, lines...)
	for _, line := range allLines {
		wrapped, err := wrapLine(pdf, line, contentWidth)
		if err != nil {
			return nil, fmt.Errorf("pdf: measure line: %w", err)
		}

		for _, w := range wrapped {
			if y+lineHeight > pageBottom {
				pdf.AddPage()
				y = marginTop
			}
			pdf.SetXY(marginLeft, y)
			if err := pdf.Cell(nil, w); err != nil {
				return nil, fmt.Errorf("pdf: write line: %w", err)
			}
			y += lineHeight
		}
	}

	out, err := pdf.GetBytesPdfReturnErr()
	if err != nil {
		return nil, fmt.Errorf("pdf: serialize: %w", err)
	}
	return out, nil
}

// wrapLine breaks s into the fewest rune runs that each fit within
// maxWidth points at the current font/size, measuring one rune at a time
// (rather than re-measuring the whole growing prefix, which would be
// quadratic in line length). Japanese text has no spaces to break on, so
// this wraps at whatever rune boundary crosses the width limit -- simpler
// than real Unicode line-breaking (kinsoku shori) rules, but every
// character stays visible, which is the property that actually mattered:
// the previous implementation dropped non-ASCII text outright.
func wrapLine(pdf *gopdf.GoPdf, s string, maxWidth float64) ([]string, error) {
	if s == "" {
		return []string{""}, nil
	}

	var lines []string
	var current []rune
	var currentWidth float64
	for _, r := range s {
		runeWidth, err := pdf.MeasureTextWidth(string(r))
		if err != nil {
			return nil, err
		}
		if currentWidth+runeWidth > maxWidth && len(current) > 0 {
			lines = append(lines, string(current))
			current = nil
			currentWidth = 0
		}
		current = append(current, r)
		currentWidth += runeWidth
	}
	if len(current) > 0 {
		lines = append(lines, string(current))
	}
	return lines, nil
}
