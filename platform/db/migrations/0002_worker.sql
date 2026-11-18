-- Fase 3: quarentena de linha venenosa no outbox (P2), identidade da mensagem só no payload (P3)
-- e a causa da falha da foto (ms-worker).
ALTER TABLE outbox DROP COLUMN topic;
ALTER TABLE outbox
    ADD COLUMN attempts   int NOT NULL DEFAULT 0,
    ADD COLUMN last_error text,
    ADD COLUMN parked_at  timestamptz;
DROP INDEX outbox_pending_idx;
CREATE INDEX outbox_pending_idx ON outbox (id) WHERE published_at IS NULL AND parked_at IS NULL;

ALTER TABLE photos
    ADD COLUMN failure_class  text,
    ADD COLUMN failure_reason text,
    ADD COLUMN finished_at    timestamptz;
