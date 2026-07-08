package writer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// LocalJSONLStore is the JSONLStore backend used when JSONL_STORE_BACKEND=local
// (the default, and the only backend before this Step F). One growing
// part-0001.jsonl file per sessions/{id}/{parts...}/ directory, appended to
// directly — this is exactly the pre-Step-F behavior, just behind the
// JSONLStore interface now. eventID is unused here: unlike GCSJSONLStore, a
// local append doesn't need a staging-object name to avoid colliding with
// concurrent writes.
type LocalJSONLStore struct {
	BaseDir string
}

func NewLocalJSONLStore(baseDir string) *LocalJSONLStore {
	return &LocalJSONLStore{BaseDir: baseDir}
}

func (s *LocalJSONLStore) Append(ctx context.Context, sessionID, eventID string, payload any, parts ...string) error {
	dir := filepath.Join(append([]string{s.BaseDir, "sessions", sessionID}, parts...)...)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	body = append(body, '\n')

	path := filepath.Join(dir, "part-0001.jsonl")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = f.Write(body)
	return err
}

// ReadAll reads {BaseDir}/sessions/{sessionID}/{parts...}/part-0001.jsonl. A
// missing file — nothing written yet for this session — returns an empty
// slice, not an error, since Post-session Job may run against a session
// that never went through every pipeline (e.g. no audio_chunk was ever
// sent, so there's no transcript/ directory).
func (s *LocalJSONLStore) ReadAll(ctx context.Context, sessionID string, parts ...string) ([][]byte, error) {
	dir := filepath.Join(append([]string{s.BaseDir, "sessions", sessionID}, parts...)...)
	path := filepath.Join(dir, "part-0001.jsonl")

	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines [][]byte
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		cp := make([]byte, len(line))
		copy(cp, line)
		lines = append(lines, cp)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return lines, nil
}
