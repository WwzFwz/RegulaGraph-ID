-- Adds durable retry availability and a bounded attempt budget to ingestion jobs.
-- The migration is metadata-only for existing rows; defaults preserve immediate eligibility once,
-- while coordinators persist later retry times. Measure claim-index behavior at corpus scale.
ALTER TABLE jobs
    ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT '-infinity',
    ADD COLUMN max_attempts integer NOT NULL DEFAULT 8 CHECK (max_attempts BETWEEN 1 AND 100);

CREATE INDEX jobs_retry_claim_idx
    ON jobs (stage, state, next_attempt_at, created_at);
