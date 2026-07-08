package writer

import (
	"context"
	"encoding/json"
	"testing"
)

type probeLine struct {
	N int `json:"n"`
}

func TestLocalJSONLStore_AppendAndReadAll(t *testing.T) {
	store := NewLocalJSONLStore(t.TempDir())
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if err := store.Append(ctx, "sess_1", "evt_ignored", probeLine{N: i}, "mood-wave"); err != nil {
			t.Fatalf("Append(%d) failed: %v", i, err)
		}
	}

	lines, err := store.ReadAll(ctx, "sess_1", "mood-wave")
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("len(lines) = %d, want 3", len(lines))
	}
	for i, line := range lines {
		var got probeLine
		if err := json.Unmarshal(line, &got); err != nil {
			t.Fatalf("unmarshal line %d: %v", i, err)
		}
		if got.N != i {
			t.Errorf("line %d: N = %d, want %d (order not preserved)", i, got.N, i)
		}
	}
}

func TestLocalJSONLStore_ReadAllMissingSessionReturnsEmpty(t *testing.T) {
	store := NewLocalJSONLStore(t.TempDir())

	lines, err := store.ReadAll(context.Background(), "sess_never_written", "mood-wave")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if lines != nil {
		t.Errorf("lines = %v, want nil for a session that was never written", lines)
	}
}

func TestLocalJSONLStore_SeparatePartsAreIsolated(t *testing.T) {
	store := NewLocalJSONLStore(t.TempDir())
	ctx := context.Background()

	if err := store.Append(ctx, "sess_1", "evt_1", probeLine{N: 1}, "mood-wave"); err != nil {
		t.Fatalf("Append mood-wave failed: %v", err)
	}
	if err := store.Append(ctx, "sess_1", "evt_2", probeLine{N: 2}, "triggers"); err != nil {
		t.Fatalf("Append triggers failed: %v", err)
	}

	moodWave, err := store.ReadAll(ctx, "sess_1", "mood-wave")
	if err != nil {
		t.Fatalf("ReadAll mood-wave failed: %v", err)
	}
	if len(moodWave) != 1 {
		t.Fatalf("len(moodWave) = %d, want 1", len(moodWave))
	}

	triggers, err := store.ReadAll(ctx, "sess_1", "triggers")
	if err != nil {
		t.Fatalf("ReadAll triggers failed: %v", err)
	}
	if len(triggers) != 1 {
		t.Fatalf("len(triggers) = %d, want 1", len(triggers))
	}
}
