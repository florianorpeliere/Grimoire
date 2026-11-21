-- +goose Up
-- SRD chunks produced by internal/srd. Session notes get their own table.
CREATE TABLE srd_chunks (
    id           text PRIMARY KEY,   -- slug of the short citation path, e.g. spells.fireball
    path         text NOT NULL,      -- short citation path, e.g. "Spells > Fireball"
    full_path    text[] NOT NULL,    -- every heading from the chapter down to the chunk's own
    title        text NOT NULL,      -- the chunk's own heading, without any [Condition] tag
    kind         text NOT NULL,
    text         text NOT NULL,      -- does not contain the chunk's own heading
    content_hash text NOT NULL,      -- hex SHA-256 of text
    srd_version  text NOT NULL,
    -- Keyword index. Title and path use "simple" (no stemming, so exact names
    -- match as written); the text uses "english" (stemmed, stop words removed).
    -- Weight labels: A title, B path, C text. The rank weight of each label is
    -- set by the query (internal/search), so it can change without re-indexing.
    search       tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple'::regconfig, title), 'A') ||
        setweight(to_tsvector('simple'::regconfig, path), 'B') ||
        setweight(to_tsvector('english'::regconfig, text), 'C')
    ) STORED
);

CREATE INDEX srd_chunks_search_idx ON srd_chunks USING gin (search);

-- +goose Down
DROP TABLE IF EXISTS srd_chunks;
