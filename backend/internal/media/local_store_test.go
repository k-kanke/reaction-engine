package media

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLocalMediaStore_Write(t *testing.T) {
	dir := t.TempDir()
	store := NewLocalMediaStore(dir, "", 0)
	store.Now = func() time.Time { return time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC) }

	ref, err := store.Write(context.Background(), "sess_1", []string{"reports", "report.pdf"}, []byte("%PDF-fake"), "application/pdf")
	if err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	wantRef := "local://sessions/sess_1/reports/report.pdf"
	if ref != wantRef {
		t.Errorf("ref = %q, want %q", ref, wantRef)
	}

	data, err := os.ReadFile(filepath.Join(dir, "sessions", "sess_1", "reports", "report.pdf"))
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}
	if string(data) != "%PDF-fake" {
		t.Errorf("file content = %q, want %q", data, "%PDF-fake")
	}

	// Write's own ref should also be readable back through Read/Exists,
	// confirming it round-trips through the same scheme Read/Exists expect.
	exists, err := store.Exists(context.Background(), ref)
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Errorf("Exists(%q) = false, want true", ref)
	}

	readBack, err := store.Read(context.Background(), ref)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if string(readBack) != "%PDF-fake" {
		t.Errorf("Read = %q, want %q", readBack, "%PDF-fake")
	}
}
