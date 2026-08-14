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
