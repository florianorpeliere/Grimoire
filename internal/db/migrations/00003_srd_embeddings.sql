-- +goose Up
-- One embedding per SRD chunk, written by "ingest srd" (internal/srd.Embed).
-- No ANN index: step 1 searches by exact scan. The dimension is fixed by the
-- column type; a model with another dimension needs a new migration.
-- embedding_model holds the Ollama tag (e.g. qwen3-embedding:0.6b), not the
-- model digest: re-pulling a changed model under the same tag is not detected.
ALTER TABLE srd_chunks
    ADD COLUMN embedding       vector(1024),
    ADD COLUMN embedding_model text,     -- model that produced embedding, e.g. qwen3-embedding:0.6b
    ADD COLUMN embedding_dim   integer,  -- its dimension, as reported by the provider
    ADD CONSTRAINT srd_chunks_embedding_meta CHECK (
        (embedding IS NULL AND embedding_model IS NULL AND embedding_dim IS NULL)
        OR (embedding IS NOT NULL AND embedding_model IS NOT NULL
            AND embedding_dim IS NOT NULL AND embedding_dim = vector_dims(embedding))
    );

-- +goose Down
ALTER TABLE srd_chunks
    DROP CONSTRAINT IF EXISTS srd_chunks_embedding_meta,
    DROP COLUMN IF EXISTS embedding_dim,
    DROP COLUMN IF EXISTS embedding_model,
    DROP COLUMN IF EXISTS embedding;
