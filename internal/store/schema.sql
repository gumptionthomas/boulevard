CREATE TABLE IF NOT EXISTS libraries (
    id             TEXT PRIMARY KEY,
    slug           TEXT NOT NULL UNIQUE,
    name           TEXT NOT NULL,
    location_label TEXT NOT NULL,
    base_url       TEXT NOT NULL,
    created_at     TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS tokens (
    id            TEXT PRIMARY KEY,
    library_id    TEXT NOT NULL REFERENCES libraries(id),
    secret        TEXT NOT NULL UNIQUE,
    period_index  INTEGER NOT NULL,
    valid_from    TEXT NOT NULL,
    valid_until   TEXT NOT NULL,
    state         TEXT NOT NULL,
    first_seen_at TEXT,
    created_at    TEXT NOT NULL,
    UNIQUE (library_id, period_index)
);

CREATE INDEX IF NOT EXISTS idx_tokens_library ON tokens(library_id, period_index);
