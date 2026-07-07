package pdf

import (
	"bytes"
	"strings"
	"testing"
)

func TestSanitizeLine(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"plain ascii", "session_id=sess_1", "session_id=sess_1"},
		{"all japanese", "直近秒で反応が下がっています", "(non-ASCII content omitted)"},
		{"mixed with ascii digits surviving", "severity=low 直近30秒", "severity=low 30 (non-ASCII content omitted)"},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeLine(tc.input); got != tc.want {
				t.Errorf("sanitizeLine(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestEscapeText(t *testing.T) {
	cases := []struct{ input, want string }{
		{"plain", "plain"},
		{"a(b)c", `a\(b\)c`},
		{`back\slash`, `back\\slash`},
	}
	for _, tc := range cases {
		if got := escapeText(tc.input); got != tc.want {
			t.Errorf("escapeText(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestRender_SinglePage(t *testing.T) {
	out, err := Render("Test Report", []string{"line one", "line two"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF-1.4")) {
		t.Errorf("output doesn't start with %%PDF-1.4 header")
	}
	if !bytes.HasSuffix(out, []byte("%%EOF")) {
		t.Errorf("output doesn't end with %%%%EOF trailer")
	}
	if got := bytes.Count(out, []byte("/Type /Page ")); got != 1 {
		t.Errorf("page count = %d, want 1 (2 lines + title well under linesPerPage)", got)
	}
	if !bytes.Contains(out, []byte("(Test Report)")) {
		t.Errorf("output doesn't contain the title text")
	}
	if !bytes.Contains(out, []byte("(line one)")) {
		t.Errorf("output doesn't contain 'line one'")
	}
}

func TestRender_MultiPage(t *testing.T) {
	lines := make([]string, linesPerPage*2+5) // forces 3 pages (title+blank pushes past 2*linesPerPage)
	for i := range lines {
		lines[i] = "line"
	}
	out, err := Render("Multi Page", lines)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	pageCount := strings.Count(string(out), "/Type /Page ")
	if pageCount != 3 {
		t.Errorf("page count = %d, want 3 (title+blank+%d lines at %d/page)", pageCount, len(lines), linesPerPage)
	}

	// /Count in the Pages object should match.
	if !strings.Contains(string(out), "/Count 3") {
		t.Errorf("output missing /Count 3 in the Pages object")
	}
}

func TestRender_NonASCIIContentOmittedNotCorrupted(t *testing.T) {
	out, err := Render("Report", []string{"直近30秒で反応が下がっています"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if bytes.Contains(out, []byte("直近")) {
		t.Errorf("raw non-ASCII bytes leaked into the PDF content stream")
	}
	if !bytes.Contains(out, []byte("non-ASCII content omitted")) {
		t.Errorf("expected the omission marker in the output")
	}
}

func TestRender_ParenthesesEscaped(t *testing.T) {
	out, err := Render("Report", []string{"severity=low (rule)"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Contains(out, []byte(`severity=low \(rule\)`)) {
		t.Errorf("parentheses in report content were not escaped in the content stream")
	}
}
