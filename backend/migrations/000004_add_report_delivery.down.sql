DROP TABLE IF EXISTS report_deliveries;

ALTER TABLE reports DROP COLUMN IF EXISTS pdf_generated_at;
ALTER TABLE reports DROP COLUMN IF EXISTS pdf_path;
