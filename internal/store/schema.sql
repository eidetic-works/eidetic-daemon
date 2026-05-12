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
