-- Eidetic Daemon W1 schema (verbatim from docs/SPEC.md § 2.2)
-- Single canonical engrams table. No FTS5, no vector embeddings, no
-- denormalized topic columns in W1. Schema additions go through ADR.

CREATE TABLE IF NOT EXISTS engrams (
  id      INTEGER PRIMARY KEY,
  surface TEXT    NOT NULL,
  ts      INTEGER NOT NULL,         -- unix epoch nanoseconds
  payload TEXT    NOT NULL,
  meta    TEXT                      -- JSON: source path, file offset, parser version
);

CREATE INDEX IF NOT EXISTS idx_surface_ts ON engrams(surface, ts DESC);

-- v0.0.14: FTS5 full-text search over payload. Non-content table (stores
-- payload text in both engrams + the FTS index) for simplicity and
-- robustness; storage overhead is acceptable for a personal-use daemon.
-- surface is UNINDEXED so we can filter on it without tokenizing.
-- Triggers keep the index in sync with the main table (insert + delete;
-- no update trigger needed — engrams are append-only via InsertBatch).
CREATE VIRTUAL TABLE IF NOT EXISTS engrams_fts USING fts5(
  payload,
  surface UNINDEXED
);

CREATE TRIGGER IF NOT EXISTS engrams_ai AFTER INSERT ON engrams BEGIN
  INSERT INTO engrams_fts(rowid, payload, surface) VALUES (new.id, new.payload, new.surface);
END;

CREATE TRIGGER IF NOT EXISTS engrams_ad AFTER DELETE ON engrams BEGIN
  DELETE FROM engrams_fts WHERE rowid = old.id;
END;
