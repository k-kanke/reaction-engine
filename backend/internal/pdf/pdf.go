// Package pdf renders plain text as a minimal, valid PDF file (single
// Type1/Helvetica font, no external dependencies, no headless browser).
// This is the "Go の PDF ライブラリ" option architecture.md's 技術選定
// allows for the PDF Renderer role — a deliberately simple stand-in,
// matching every other local stub in this migration (fake STT, fake LLM,
// local media storage), not a general-purpose PDF engine.
//
// The base 14 Helvetica font only covers WinAnsi/Latin-1 text; see
// sanitizeLine for how non-ASCII content (e.g. Japanese feedback messages)
// is handled rather than silently corrupted.
package pdf

import (
	"bytes"
	"fmt"
	"strings"
)

const (
	pageWidth    = 612 // US Letter, points (1/72 inch)
	pageHeight   = 792
	marginLeft   = 50
	marginTop    = 750
	fontSize     = 11
	lineHeight   = 14
	linesPerPage = 45 // (marginTop - bottomMargin) / lineHeight, rounded down
)

// Render builds a multi-page PDF with title as the first line of the first
// page followed by lines, one PDF text line each. Long lines are not
// wrapped — architecture.md's report content (short structured labels,
// counts, timestamps) doesn't need it; callers should pre-wrap free text.
func Render(title string, lines []string) ([]byte, error) {
	allLines := append([]string{title, ""}, lines...)
	pages := paginate(allLines, linesPerPage)
	if len(pages) == 0 {
		pages = [][]string{{}}
	}

	b := &builder{}
	b.buf.WriteString("%PDF-1.4\n")

	catalogObj := b.nextObjNum()
	pagesObj := b.nextObjNum()
	fontObj := b.nextObjNum()

	pageObjs := make([]int, len(pages))
	contentObjs := make([]int, len(pages))
	for i := range pages {
		pageObjs[i] = b.nextObjNum()
		contentObjs[i] = b.nextObjNum()
	}

	b.writeObj(catalogObj, fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pagesObj))

	kids := make([]string, len(pageObjs))
	for i, num := range pageObjs {
		kids[i] = fmt.Sprintf("%d 0 R", num)
	}
	b.writeObj(pagesObj, fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(pageObjs)))

	b.writeObj(fontObj, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")

	for i, page := range pages {
		b.writeObj(pageObjs[i], fmt.Sprintf(
			"<< /Type /Page /Parent %d 0 R /Resources << /Font << /F1 %d 0 R >> >> /MediaBox [0 0 %d %d] /Contents %d 0 R >>",
			pagesObj, fontObj, pageWidth, pageHeight, contentObjs[i],
		))

		stream := buildContentStream(page)
		b.writeObj(contentObjs[i], fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(stream), stream))
	}

	xrefOffset := b.buf.Len()
	totalObjs := b.objCount + 1 // + object 0
	b.buf.WriteString("xref\n")
	fmt.Fprintf(&b.buf, "0 %d\n", totalObjs)
	b.buf.WriteString("0000000000 65535 f \n")
	for i := 1; i <= b.objCount; i++ {
		fmt.Fprintf(&b.buf, "%010d 00000 n \n", b.offsets[i])
	}

	b.buf.WriteString("trailer\n")
	fmt.Fprintf(&b.buf, "<< /Size %d /Root %d 0 R >>\n", totalObjs, catalogObj)
	b.buf.WriteString("startxref\n")
	fmt.Fprintf(&b.buf, "%d\n", xrefOffset)
	b.buf.WriteString("%%EOF")

	return b.buf.Bytes(), nil
}

// paginate splits lines into chunks of at most perPage lines.
func paginate(lines []string, perPage int) [][]string {
	if len(lines) == 0 {
		return nil
	}
	var pages [][]string
	for len(lines) > 0 {
		n := perPage
		if n > len(lines) {
			n = len(lines)
		}
		pages = append(pages, lines[:n])
		lines = lines[n:]
	}
	return pages
}

// buildContentStream renders lines top-down starting at marginTop, one PDF
// Tj text-show operator per line, using relative Td moves so no per-line
// absolute-position bookkeeping is needed.
func buildContentStream(lines []string) string {
	var s strings.Builder
	fmt.Fprintf(&s, "BT\n/F1 %d Tf\n%d %d Td\n%d TL\n", fontSize, marginLeft, marginTop, lineHeight)
	for i, line := range lines {
		if i > 0 {
			s.WriteString("T*\n")
		}
		fmt.Fprintf(&s, "(%s) Tj\n", escapeText(sanitizeLine(line)))
	}
	s.WriteString("ET\n")
	return s.String()
}

// sanitizeLine drops characters outside the base-14 Helvetica font's
// printable ASCII range (WinAnsi covers more, but staying to plain ASCII
// avoids any encoding ambiguity). Report content in this system is
// routinely Japanese (feedback messages, transcript excerpts); rather than
// emit corrupted/garbled bytes into a PDF viewer, this stub keeps whatever
// ASCII structure survives (labels, numbers, enum values like
// "severity=low") and flags that something was left out. Real Unicode/CJK
// rendering needs an embedded CID font or an HTML->PDF renderer — out of
// scope for this local stand-in, matching every other Phase 0-13 stub's
// "deterministic and simple over complete" precedent.
func sanitizeLine(s string) string {
	var b strings.Builder
	hadNonASCII := false
	for _, r := range s {
		if r >= 32 && r < 127 {
			b.WriteRune(r)
		} else {
			hadNonASCII = true
		}
	}
	out := strings.TrimSpace(b.String())
	if hadNonASCII {
		if out == "" {
			return "(non-ASCII content omitted)"
		}
		return out + " (non-ASCII content omitted)"
	}
	return out
}

// escapeText escapes the three characters PDF literal strings ( ... )
// treat specially.
func escapeText(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `(`, `\(`)
	s = strings.ReplaceAll(s, `)`, `\)`)
	return s
}

// builder accumulates PDF object bytes and their offsets so the trailing
// xref table can point back to each one.
type builder struct {
	buf      bytes.Buffer
	objCount int
	offsets  []int // 1-indexed; offsets[0] unused
}

func (b *builder) nextObjNum() int {
	b.objCount++
	if len(b.offsets) <= b.objCount {
		b.offsets = append(b.offsets, make([]int, b.objCount-len(b.offsets)+1)...)
	}
	return b.objCount
}

func (b *builder) writeObj(num int, body string) {
	b.offsets[num] = b.buf.Len()
	fmt.Fprintf(&b.buf, "%d 0 obj\n%s\nendobj\n", num, body)
}
