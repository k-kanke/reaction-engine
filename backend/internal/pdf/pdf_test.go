package pdf

import (
	"bytes"
	"strings"
	"testing"

	"github.com/signintech/gopdf"
)

func TestRender_ValidPDFHeader(t *testing.T) {
	out, err := Render("Test Report", []string{"line one", "line two"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-")) {
		t.Errorf("output doesn't start with a %%PDF- header")
	}
}

func TestRender_JapaneseContentDoesNotError(t *testing.T) {
	out, err := Render("レポート", []string{
		"直近30秒で反応が下がっています",
		"severity=low source=rule",
		"重要なウィンドウ: 発言内容を踏まえた要約がここに入ります。",
	})
	if err != nil {
		t.Fatalf("unexpected error rendering Japanese content: %v", err)
	}
	if len(out) == 0 {
		t.Errorf("expected non-empty output")
	}
}

func TestRender_MultiPage(t *testing.T) {
	lines := make([]string, 80)
	for i := range lines {
		lines[i] = "line"
	}
	out, err := Render("Multi Page", lines)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// "/Type /Page\n" (note the trailing newline) matches only leaf Page
	// objects, not the "/Type /Pages" container object.
	pageCount := strings.Count(string(out), "/Type /Page\n")
	if pageCount < 2 {
		t.Errorf("page count = %d, want at least 2 for %d lines", pageCount, len(lines))
	}
}

func TestWrapLine_StaysWithinWidth(t *testing.T) {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := pdf.AddTTFFontData(fontFamily, notoSansJPFont); err != nil {
		t.Fatalf("load font: %v", err)
	}
	if err := pdf.SetFont(fontFamily, "", fontSize); err != nil {
		t.Fatalf("set font: %v", err)
	}

	const maxWidth = 100.0
	input := "直近30秒で反応が下がっています。これは十分に長い一文で複数行に折り返されるはずです。"

	wrapped, err := wrapLine(pdf, input, maxWidth)
	if err != nil {
		t.Fatalf("wrapLine: %v", err)
	}
	if len(wrapped) < 2 {
		t.Fatalf("expected wrapping to produce multiple lines, got %d", len(wrapped))
	}

	var rejoined strings.Builder
	for _, w := range wrapped {
		width, err := pdf.MeasureTextWidth(w)
		if err != nil {
			t.Fatalf("measure wrapped line: %v", err)
		}
		if width > maxWidth {
			t.Errorf("wrapped line %q has width %.1f, want <= %.1f", w, width, maxWidth)
		}
		rejoined.WriteString(w)
	}
	if rejoined.String() != input {
		t.Errorf("wrapped lines rejoined = %q, want %q (no characters lost)", rejoined.String(), input)
	}
}

func TestWrapLine_Empty(t *testing.T) {
	pdf := &gopdf.GoPdf{}
	pdf.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := pdf.AddTTFFontData(fontFamily, notoSansJPFont); err != nil {
		t.Fatalf("load font: %v", err)
	}
	if err := pdf.SetFont(fontFamily, "", fontSize); err != nil {
		t.Fatalf("set font: %v", err)
	}

	got, err := wrapLine(pdf, "", 100.0)
	if err != nil {
		t.Fatalf("wrapLine: %v", err)
	}
	if len(got) != 1 || got[0] != "" {
		t.Errorf("wrapLine(\"\") = %v, want a single empty line", got)
	}
}

func TestStyleForLine(t *testing.T) {
	cases := []struct {
		name        string
		raw         string
		wantSize    float64
		wantIndent  float64
		wantMarker  string
		wantContent string
	}{
		{"h1", "# Title", heading1FontSize, 0, "", "Title"},
		{"h2", "## 総評", heading2FontSize, 0, "", "総評"},
		{"h3", "### 詳細", heading3FontSize, 0, "", "詳細"},
		{"bullet dash", "- 発言の抜粋", fontSize, bulletIndent, bulletMarker, "発言の抜粋"},
		{"bullet star", "* 発言の抜粋", fontSize, bulletIndent, bulletMarker, "発言の抜粋"},
		{"nested bullet dash", "  - 発言: 抜粋", fontSize, bulletIndent * 2, bulletMarker, "発言: 抜粋"},
		{"nested bullet star", "  * 発言: 抜粋", fontSize, bulletIndent * 2, bulletMarker, "発言: 抜粋"},
		{"plain", "反応は概ね安定しています。", fontSize, 0, "", "反応は概ね安定しています。"},
		{"empty", "", fontSize, 0, "", ""},
		{"bold markers stripped", "これは**重要**です", fontSize, 0, "", "これは重要です"},
		{"heading with bold stripped", "## **総評**", heading2FontSize, 0, "", "総評"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			style, content := styleForLine(tc.raw)
			if style.fontSize != tc.wantSize {
				t.Errorf("fontSize = %v, want %v", style.fontSize, tc.wantSize)
			}
			if style.indent != tc.wantIndent {
				t.Errorf("indent = %v, want %v", style.indent, tc.wantIndent)
			}
			if style.marker != tc.wantMarker {
				t.Errorf("marker = %q, want %q", style.marker, tc.wantMarker)
			}
			if content != tc.wantContent {
				t.Errorf("content = %q, want %q", content, tc.wantContent)
			}
		})
	}
}
