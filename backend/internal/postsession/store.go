package postsession

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/k-kanke/reaction-engine/backend/internal/contract"
)

// ParticipantBaseline mirrors the columns of one participant_baselines row
// Post-session Job needs.
type ParticipantBaseline struct {
	AudienceID  string
	Baseline    json.RawMessage
	SampleCount int
	Confidence  float64
}

// VisualSummary mirrors the columns of one visual_summaries row.
type VisualSummary struct {
	AudienceID    string
	CaptureID     string
	VisualSummary json.RawMessage
	Confidence    *float64
}

// Transcript mirrors the columns of one transcripts row.
type Transcript struct {
	Speaker    string
	TStartMs   int64
	TEndMs     int64
	Text       string
	Confidence *float64
	IsFinal    bool
}

// SignalSummary mirrors the columns of one signal_summaries row. Nothing
// writes to this table yet as of Phase 13 (the real system-computation
// signal_summary layer is still aspirational per architecture.md), so this
// will read back empty for every session today; Post-session Job still
// reads it so it picks summaries up automatically once a later phase
// starts writing them.
type SignalSummary struct {
	AudienceID string
	TMs        int64
	Summary    json.RawMessage
}

// PGStore is the pgx-backed persistence boundary Post-session Job (Phase
// 13 of plan/backend-local-docker-runbook.md) reads its Postgres inputs
// from and writes the generated report to.
type PGStore struct {
	pool *pgxpool.Pool
}

func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool}
}

// EnsureSession upserts a minimal sessions row so a report (FK on
// session_id) can be inserted even for a session that never went through
// media-api/writer's own EnsureSession calls. Mirrors
// internal/media.PGStore.EnsureSession.
func (s *PGStore) EnsureSession(ctx context.Context, sessionID string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO sessions (session_id, meeting_provider)
		VALUES ($1, 'unknown')
		ON CONFLICT (session_id) DO NOTHING
	`, sessionID)
	return err
}

func (s *PGStore) ListParticipantBaselines(ctx context.Context, sessionID string) ([]ParticipantBaseline, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT audience_id, baseline, sample_count, confidence
		FROM participant_baselines
		WHERE session_id = $1
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ParticipantBaseline
	for rows.Next() {
		var b ParticipantBaseline
		if err := rows.Scan(&b.AudienceID, &b.Baseline, &b.SampleCount, &b.Confidence); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *PGStore) ListVisualSummaries(ctx context.Context, sessionID string) ([]VisualSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT audience_id, capture_id, visual_summary, confidence
		FROM visual_summaries
		WHERE session_id = $1
		ORDER BY created_at
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []VisualSummary
	for rows.Next() {
		var v VisualSummary
		var captureID *string
		if err := rows.Scan(&v.AudienceID, &captureID, &v.VisualSummary, &v.Confidence); err != nil {
			return nil, err
		}
		if captureID != nil {
			v.CaptureID = *captureID
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *PGStore) ListTranscripts(ctx context.Context, sessionID string) ([]Transcript, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT speaker, t_start_ms, t_end_ms, text, confidence, is_final
		FROM transcripts
		WHERE session_id = $1
		ORDER BY t_start_ms
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Transcript
	for rows.Next() {
		var t Transcript
		if err := rows.Scan(&t.Speaker, &t.TStartMs, &t.TEndMs, &t.Text, &t.Confidence, &t.IsFinal); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListTriggerEvents returns every trigger_events row for sessionID, oldest
// first — the anchors BuildReport (Step 10) slices important_windows
// around.
func (s *PGStore) ListTriggerEvents(ctx context.Context, sessionID string) ([]contract.TriggerEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT event_id, session_id, trigger_id, type, source, t_ms, peak_t_ms, delta
		FROM trigger_events
		WHERE session_id = $1
		ORDER BY t_ms
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []contract.TriggerEvent
	for rows.Next() {
		var t contract.TriggerEvent
		if err := rows.Scan(&t.EventID, &t.SessionID, &t.TriggerID, &t.Type, &t.Source, &t.TMs, &t.PeakTMs, &t.Delta); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListFeedbackEvents returns every feedback_events row for sessionID,
// oldest first, for BuildReport's realtime_feedback_history (Step 10).
func (s *PGStore) ListFeedbackEvents(ctx context.Context, sessionID string) ([]contract.FeedbackEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT event_id, session_id, audience_id, t_ms, feedback_type, severity, message,
		       reason_codes, evidence_quote, source, model_version, confidence, cooldown_ms
		FROM feedback_events
		WHERE session_id = $1
		ORDER BY t_ms
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []contract.FeedbackEvent
	for rows.Next() {
		var f contract.FeedbackEvent
		var audienceID, modelVersion *string
		var reasonCodes json.RawMessage
		if err := rows.Scan(
			&f.EventID, &f.SessionID, &audienceID, &f.TMs, &f.FeedbackType, &f.Severity, &f.Message,
			&reasonCodes, &f.EvidenceQuote, &f.Source, &modelVersion, &f.Confidence, &f.CooldownMs,
		); err != nil {
			return nil, err
		}
		f.Type = "feedback_event"
		if audienceID != nil {
			f.AudienceID = *audienceID
		}
		if modelVersion != nil {
			f.ModelVersion = *modelVersion
		}
		if len(reasonCodes) > 0 {
			if err := json.Unmarshal(reasonCodes, &f.ReasonCodes); err != nil {
				return nil, err
			}
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ListEvidenceMediaRefs returns every uploaded evidence_frame media_ref for
// sessionID, paired with the trigger_id it was captured for (media_refs
// rows with a NULL trigger_id — i.e. baseline_frame captures — are
// excluded by the WHERE clause, not just by purpose, since only
// evidence_frame captures set it, per Step 7).
func (s *PGStore) ListEvidenceMediaRefs(ctx context.Context, sessionID string) ([]EvidenceMediaRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT trigger_id, media_ref
		FROM media_refs
		WHERE session_id = $1 AND purpose = 'evidence_frame' AND upload_status = 'uploaded' AND trigger_id IS NOT NULL
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []EvidenceMediaRef
	for rows.Next() {
		var ref EvidenceMediaRef
		if err := rows.Scan(&ref.TriggerID, &ref.MediaRef); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

func (s *PGStore) ListSignalSummaries(ctx context.Context, sessionID string) ([]SignalSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT audience_id, t_ms, summary
		FROM signal_summaries
		WHERE session_id = $1
		ORDER BY t_ms
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SignalSummary
	for rows.Next() {
		var summary SignalSummary
		if err := rows.Scan(&summary.AudienceID, &summary.TMs, &summary.Summary); err != nil {
			return nil, err
		}
		out = append(out, summary)
	}
	return out, rows.Err()
}

// InsertReport inserts one reports row and returns its generated id.
func (s *PGStore) InsertReport(ctx context.Context, sessionID string, report json.RawMessage, generatedAt time.Time) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO reports (session_id, report, generated_at)
		VALUES ($1, $2::jsonb, $3)
		RETURNING id
	`, sessionID, []byte(report), generatedAt).Scan(&id)
	return id, err
}

// GetLatestReport returns the most recently generated_at reports row for
// sessionID (Step 11 of plan/mood-wave-contract-migration.md's
// pdf-renderer reads this to build the PDF).
func (s *PGStore) GetLatestReport(ctx context.Context, sessionID string) (id string, report json.RawMessage, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT id, report
		FROM reports
		WHERE session_id = $1
		ORDER BY generated_at DESC
		LIMIT 1
	`, sessionID).Scan(&id, &report)
	return id, report, err
}

// UpdateReportPDF records where pdf-renderer wrote the report's PDF
// (Step 11). pdfPath is a media_ref-style reference (local://... or, once
// GCS is wired, gs://...), matching how capture_snapshots/media_refs
// store media_ref rather than a raw filesystem path.
func (s *PGStore) UpdateReportPDF(ctx context.Context, reportID, pdfPath string, generatedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE reports SET pdf_path = $2, pdf_generated_at = $3
		WHERE id = $1
	`, reportID, pdfPath, generatedAt)
	return err
}

// GetReportPDFPath returns the latest report's id and pdf_path for
// sessionID (Step 11's gmail-sender reads this; pdfPath is "" if
// pdf-renderer hasn't run yet).
func (s *PGStore) GetReportPDFPath(ctx context.Context, sessionID string) (reportID, pdfPath string, err error) {
	var path *string
	err = s.pool.QueryRow(ctx, `
		SELECT id, pdf_path
		FROM reports
		WHERE session_id = $1
		ORDER BY generated_at DESC
		LIMIT 1
	`, sessionID).Scan(&reportID, &path)
	if err != nil {
		return "", "", err
	}
	if path != nil {
		pdfPath = *path
	}
	return reportID, pdfPath, nil
}

// InsertReportDelivery inserts one report_deliveries row (migration
// 000004_add_report_delivery, Step 11) and returns its generated id.
// sentAt/errMsg are nil for a failed delivery attempt that never sent.
func (s *PGStore) InsertReportDelivery(ctx context.Context, reportID, recipient, status string, sentAt *time.Time, errMsg *string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO report_deliveries (report_id, recipient, status, sent_at, error)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, reportID, recipient, status, sentAt, errMsg).Scan(&id)
	return id, err
}
