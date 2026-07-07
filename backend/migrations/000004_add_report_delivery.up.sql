ALTER TABLE reports ADD COLUMN pdf_path text;
ALTER TABLE reports ADD COLUMN pdf_generated_at timestamptz;

CREATE TABLE report_deliveries (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    report_id uuid NOT NULL REFERENCES reports (id),
    recipient text NOT NULL,
    status text NOT NULL,
    sent_at timestamptz,
    error text,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_report_deliveries_report ON report_deliveries (report_id);
