CREATE TABLE batches (
    id               uuid PRIMARY KEY,
    idempotency_key  text UNIQUE,
    request_hash     text,
    total            int NOT NULL CHECK (total > 0),
    completed_count  int NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL DEFAULT now(),
    completed_at     timestamptz
);

CREATE TABLE photos (
    id          uuid PRIMARY KEY,
    batch_id    uuid NOT NULL REFERENCES batches (id),
    source_url  text NOT NULL,
    status      text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done', 'failed')),
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX photos_batch_id_idx ON photos (batch_id);

CREATE TABLE outbox (
    id            bigserial PRIMARY KEY,
    topic         text NOT NULL,
    routing_key   text NOT NULL,
    payload       jsonb NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    published_at  timestamptz
);
CREATE INDEX outbox_pending_idx ON outbox (id) WHERE published_at IS NULL;

CREATE TABLE events (
    seq         bigserial PRIMARY KEY,
    type        text NOT NULL,
    batch_id    uuid,
    photo_id    uuid,
    payload     jsonb NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now()
);
