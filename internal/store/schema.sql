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
    -- This unique constraint gets an automatic index on
    -- (library_id, period_index), which is exactly the lookup every query
    -- here makes. A second hand-written index on the same columns would be
    -- byte-for-byte redundant.
    UNIQUE (library_id, period_index)
);

CREATE TABLE IF NOT EXISTS sessions (
    id          TEXT PRIMARY KEY,
    library_id  TEXT NOT NULL REFERENCES libraries(id),
    token_id    TEXT NOT NULL REFERENCES tokens(id),
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL
    -- No index beyond the primary key. Every lookup is by id, which
    -- PRIMARY KEY already indexes, and expiry is checked on read rather
    -- than swept, so nothing ever queries expires_at.
);
