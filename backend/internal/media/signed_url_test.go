package media

import (
	"testing"
	"time"
)

func TestExtensionForContentType(t *testing.T) {
	cases := []struct {
		contentType string
		wantExt     string
		wantOK      bool
	}{
		{"image/webp", "webp", true},
		{"image/jpeg", "jpg", true},
		{"image/png", "png", true},
		{"image/gif", "", false},
		{"", "", false},
	}

	for _, tc := range cases {
		ext, ok := extensionForContentType(tc.contentType)
		if ok != tc.wantOK || ext != tc.wantExt {
			t.Errorf("extensionForContentType(%q) = (%q, %v), want (%q, %v)", tc.contentType, ext, ok, tc.wantExt, tc.wantOK)
		}
	}
}

func TestMediaRef(t *testing.T) {
	got := mediaRef("sess_1", "cap_1", "webp")
	want := "local://sessions/sess_1/baseline/frames/cap_1.webp"
	if got != want {
		t.Errorf("mediaRef() = %q, want %q", got, want)
	}
}

func TestLocalUploadURL(t *testing.T) {
	got := localUploadURL("http://localhost:8081", "sess_1", "cap_1", "webp")
	want := "http://localhost:8081/local-upload/sess_1/cap_1.webp"
	if got != want {
		t.Errorf("localUploadURL() = %q, want %q", got, want)
	}
}

func TestExpiresAt(t *testing.T) {
	now := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	got := expiresAt(900*time.Second, now)
	want := "2026-07-04T12:15:00Z"
	if got != want {
		t.Errorf("expiresAt() = %q, want %q", got, want)
	}
}
