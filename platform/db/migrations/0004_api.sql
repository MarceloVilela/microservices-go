-- Fase 6 (ms-api): (1) NOTIFY a cada evento novo, para o SSE acordar sem polling (ADR 0016);
-- o NOTIFY só acorda, a verdade é a leitura por seq. Ingest e worker não mudam.
CREATE FUNCTION events_notify() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_notify('events', NEW.seq::text);
    RETURN NEW;
END
$$;
CREATE TRIGGER events_notify AFTER INSERT ON events FOR EACH ROW EXECUTE FUNCTION events_notify();
CREATE INDEX events_created_at_idx ON events (created_at);

-- (2) quantas vezes a foto foi fechada (done/failed). O worker só conta o lote na 1ª: reprocessar
-- uma foto da DLQ não decrementa nem incrementa completed_count (ADR 0018, D3).
ALTER TABLE photos ADD COLUMN attempts_closed int NOT NULL DEFAULT 0;
UPDATE photos SET attempts_closed = 1 WHERE status IN ('done', 'failed');

-- (3) paginação por cursor (created_at, id) em ordem decrescente.
CREATE INDEX batches_created_idx ON batches (created_at DESC, id DESC);
CREATE INDEX photos_created_idx ON photos (created_at DESC, id DESC);
CREATE INDEX photos_failed_idx ON photos (finished_at DESC, id DESC) WHERE status = 'failed';
