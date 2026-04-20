-- +goose Up

CREATE TABLE runs (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    goal         TEXT        NOT NULL,
    status       TEXT        NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'running', 'completed', 'failed', 'cancelled')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    completed_at TIMESTAMPTZ
);

CREATE TABLE tasks (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id       UUID        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    kind         TEXT        NOT NULL
                 CHECK (kind IN ('ai', 'fetch', 'transform')),
    spec         JSONB       NOT NULL DEFAULT '{}'::JSONB,
    depends_on   UUID[]      NOT NULL DEFAULT ARRAY[]::UUID[],
    status       TEXT        NOT NULL DEFAULT 'pending'
                 CHECK (status IN ('pending', 'ready', 'running', 'completed', 'failed', 'cancelled')),
    result       JSONB,
    error        TEXT,
    attempts     INT         NOT NULL DEFAULT 0,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at   TIMESTAMPTZ,
    completed_at TIMESTAMPTZ
);

-- Dispatcher query: "find tasks in this run that are ready to run."
CREATE INDEX tasks_run_status_idx ON tasks (run_id, status);

CREATE TABLE events (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    -- seq gives a cheap monotonic cursor for SSE resume.
    -- unique_rowid() is CRDB's distributed-safe sequence; don't use SERIAL.
    seq        INT8        NOT NULL DEFAULT unique_rowid(),
    run_id     UUID        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    task_id    UUID        REFERENCES tasks(id) ON DELETE CASCADE,
    kind       TEXT        NOT NULL,
    payload    JSONB       NOT NULL DEFAULT '{}'::JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- SSE cursor query: "events for this run after seq X, in order."
CREATE INDEX events_run_seq_idx ON events (run_id, seq);

-- +goose Down

DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS tasks;
DROP TABLE IF EXISTS runs;
