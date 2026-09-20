-- Extends durable job/checkpoint stage constraints for Go-owned BIND and Rust-owned CHUNK.
-- Values are appended for protobuf compatibility; application stage rank defines execution order.
-- The constraint rewrite takes a table lock and scans existing rows. It is transactional and
-- replay-safe through the migration checksum ledger; production rollout must schedule lock impact.
ALTER TABLE jobs DROP CONSTRAINT jobs_stage_check;
ALTER TABLE jobs ADD CONSTRAINT jobs_stage_check CHECK (stage BETWEEN 0 AND 9);

ALTER TABLE job_checkpoints DROP CONSTRAINT job_checkpoints_stage_check;
ALTER TABLE job_checkpoints ADD CONSTRAINT job_checkpoints_stage_check CHECK (stage BETWEEN 1 AND 9);
