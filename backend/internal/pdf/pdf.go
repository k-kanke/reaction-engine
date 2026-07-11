// Package pdf renders a lightweight Markdown subset as a PDF file, with an
// embedded CJK font so Japanese report content (feedback messages,
// transcript excerpts -- the majority of this system's actual text)
// renders correctly instead of being dropped. This replaces the earlier
// base-14-Helvetica-only, hand rolled PDF writer
// (plan/post-session-report-implementation.md Step 7), which could only
// emit plain ASCII and silently discarded everything else.
//
// The Markdown subset understood is: "# "/"## "/"### " headings (larger
// font size, no separate bold weight), "- "/"* " bullets (indented with a
// "• " marker), and blank lines as paragraph spacing. "**bold**" markers
// are stripped rather than rendered bold -- only one font weight
// (Regular) is embedded, and true inline bold would need both a second
// embedded TTF and style-aware line wrapping. Everything else is a plain
// wrapped paragraph, as before.
package pdf

import (
	_ "embed"
	"fmt"
	"strings"

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

	heading1FontSize   = 20.0
	heading1LineHeight = 26.0
	heading2FontSize   = 15.0
	heading2LineHeight = 20.0
	heading3FontSize   = 12.5
	heading3LineHeight = 17.0

	bulletIndent = 14.0
	bulletMarker = "• "
)

// lineStyle is the resolved rendering style for one Markdown-ish input
// line: font size/line height (headings get larger sizes than body text),
// left indent, and an optional marker (e.g. a bullet) drawn once before
// the line's first wrapped chunk.
type lineStyle struct {
	fontSize   float64
	lineHeight float64
	indent     float64
	marker     string
}

// styleForLine inspects raw's Markdown-ish prefix and returns the style to
// render it with, plus its content with that prefix and any "**" bold
// markers removed. A "  - "/"  * " prefix (two leading spaces) is a
// nested bullet, rendered at double bulletIndent -- e.g. sub-details under
// an important_windows entry (cmd/pdf-renderer's reportToLines).
func styleForLine(raw string) (lineStyle, string) {
	switch {
	case strings.HasPrefix(raw, "### "):
		return lineStyle{fontSize: heading3FontSize, lineHeight: heading3LineHeight}, stripBoldMarkers(strings.TrimPrefix(raw, "### "))
	case strings.HasPrefix(raw, "## "):
		return lineStyle{fontSize: heading2FontSize, lineHeight: heading2LineHeight}, stripBoldMarkers(strings.TrimPrefix(raw, "## "))
	case strings.HasPrefix(raw, "# "):
		return lineStyle{fontSize: heading1FontSize, lineHeight: heading1LineHeight}, stripBoldMarkers(strings.TrimPrefix(raw, "# "))
	case strings.HasPrefix(raw, "  - "), strings.HasPrefix(raw, "  * "):
		return lineStyle{fontSize: fontSize, lineHeight: lineHeight, indent: bulletIndent * 2, marker: bulletMarker}, stripBoldMarkers(raw[4:])
	case strings.HasPrefix(raw, "- "):
		return lineStyle{fontSize: fontSize, lineHeight: lineHeight, indent: bulletIndent, marker: bulletMarker}, stripBoldMarkers(strings.TrimPrefix(raw, "- "))
	case strings.HasPrefix(raw, "* "):
		return lineStyle{fontSize: fontSize, lineHeight: lineHeight, indent: bulletIndent, marker: bulletMarker}, stripBoldMarkers(strings.TrimPrefix(raw, "* "))
	default:
		return lineStyle{fontSize: fontSize, lineHeight: lineHeight}, stripBoldMarkers(raw)
	}
}

func stripBoldMarkers(s string) string {
	return strings.ReplaceAll(s, "**", "")
}

// Render builds a multi-page PDF with title as a level-1 heading on the
// first page followed by lines, each interpreted per styleForLine and
// word-wrapped (see wrapLine) to fit the page width. Pagination happens
// automatically once a page's content area is full.
func Render(title string, lines []string) ([]byte, error) {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})

	if err := pdf.AddTTFFontData(fontFamily, notoSansJPFont); err != nil {
		return nil, fmt.Errorf("pdf: load embedded font: %w", err)
	}
	if err := pdf.SetFont(fontFamily, "", fontSize); err != nil {
		return nil, fmt.Errorf("pdf: set font: %w", err)
	}

	pageBottom := gopdf.PageSizeA4.H - marginBottom
	pdf.AddPage()
	y := marginTop

	allLines := append([]string{"# " + title, ""}, lines...)
	for _, raw := range allLines {
		style, content := styleForLine(raw)

		if err := pdf.SetFontSize(style.fontSize); err != nil {
			return nil, fmt.Errorf("pdf: set font size: %w", err)
		}

		markerWidth := 0.0
		if style.marker != "" {
			w, err := pdf.MeasureTextWidth(style.marker)
			if err != nil {
				return nil, fmt.Errorf("pdf: measure marker: %w", err)
			}
			markerWidth = w
		}

		x := marginLeft + style.indent
		contentWidth := gopdf.PageSizeA4.W - x - marginRight - markerWidth

		wrapped, err := wrapLine(pdf, content, contentWidth)
		if err != nil {
			return nil, fmt.Errorf("pdf: measure line: %w", err)
		}

		for i, w := range wrapped {
			if y+style.lineHeight > pageBottom {
				pdf.AddPage()
				y = marginTop
			}
			if i == 0 && style.marker != "" {
				pdf.SetXY(x, y)
				if err := pdf.Cell(nil, style.marker); err != nil {
					return nil, fmt.Errorf("pdf: write marker: %w", err)
				}
			}
			pdf.SetXY(x+markerWidth, y)
			if err := pdf.Cell(nil, w); err != nil {
				return nil, fmt.Errorf("pdf: write line: %w", err)
			}
			y += style.lineHeight
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
