-- Fase 5: deduplicação do ms-notification (um registro por lote notificado). Sem FK para batches:
-- é tabela do serviço, e o evento pode chegar depois de qualquer limpeza do lado do ingest.
CREATE TABLE notifications (
    batch_id     uuid PRIMARY KEY,
    notified_at  timestamptz NOT NULL DEFAULT now(),
    last_error   text
);
