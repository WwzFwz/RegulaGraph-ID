-- Separates the monotonic worker attempt from each stage's retry budget.
-- Existing jobs keep their current attempt as the active stage count so an upgrade does not silently
-- grant extra retries. Measure claim-index behavior and exhausted cleanup under contention.
ALTER TABLE jobs
    ADD COLUMN stage_attempt integer NOT NULL DEFAULT 0 CHECK (stage_attempt BETWEEN 0 AND 100);

UPDATE jobs SET stage_attempt = LEAST(attempt, 100);
