-- +goose Up
CREATE TABLE queue_messages (
    id TEXT PRIMARY KEY,
    topic TEXT NOT NULL,
    body BYTEA NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts INTEGER NOT NULL CHECK (max_attempts > 0),
    available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    locked_by TEXT,
    locked_until TIMESTAMPTZ,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);
CREATE INDEX idx_queue_messages_ready ON queue_messages(topic, available_at, created_at, id) WHERE status = 'pending';
CREATE INDEX idx_queue_messages_expired ON queue_messages(topic, locked_until, id) WHERE status = 'processing';
CREATE INDEX idx_queue_messages_failed ON queue_messages(topic, updated_at) WHERE status = 'failed';

-- +goose Down
DROP TABLE queue_messages;
