-- Persists the worker completion outcome beside each checkpoint for unambiguous crash recovery.
-- NULL preserves legacy checkpoints, which must never be inferred as successful.
ALTER TABLE job_checkpoints
    ADD COLUMN terminal_status smallint CHECK (terminal_status BETWEEN 1 AND 3);
