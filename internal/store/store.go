// Package store wraps SQLite-WAL access for the daemon. Two pools per
// ADR-014 pattern #3: a single-conn writer (eliminates "database is locked"
// cascade) and a read-only multi-conn reader pool. Driver is modernc.org/sqlite
// (pure-Go) per ADR-016 — cross-compile-clean default.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "embed"
	_ "modernc.org/sqlite"

	"github.com/eidetic-works/eidetic-daemon/internal/engram"
)

//go:embed schema.sql
var schemaSQL string

// Open string pragmas per ADR-014 pattern #1. WAL is non-negotiable;
// synchronous=NORMAL trades durability-window for speed (acceptable for
// append-only audit-shaped store); busy_timeout masks transient lock
// contention without escalating to error handling.
const writerPragmas = "_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=cache_size(-64000)"

// Reader pool runs in mode=ro so journal_mode + synchronous are no-ops; only
// busy_timeout + cache_size apply per cc-peer PR#1 minor #1.
const readerPragmas = "_pragma=busy_timeout(5000)&_pragma=cache_size(-64000)"

// readerPoolSize covers 5 expected surfaces + headroom (per ADR-014 pattern #3).
const readerPoolSize = 8

// MaxPayloadBytes caps Insert payload to prevent a single oversized engram
// (e.g., a 10MB Cursor JSONL chunk from a Phase-3 parser bug) from blocking
// ALL writers under the SetMaxOpenConns(1) writer-pool shape per cc-peer
// PR#1 concern #3. 1 MiB covers realistic engram size with ~10× headroom over
// typical surface payloads (300-5KB observed).
const MaxPayloadBytes = 1 << 20

// Store owns the writer + reader pool pair. Always opened together against
// the same DB file. Callers MUST Close() to release both pools.
type Store struct {
	writer *sql.DB
	reader *sql.DB
	path   string
}

// Open initializes the engrams.db at path (or default ~/.eidetic/engrams.db
// if path is empty), runs schema migration, and returns a Store with both
// pools live. Cold-start cost on modernc is ~1.75s per ADR-016 — caller
// should invoke Open at app startup, not on first user request.
func Open(path string) (*Store, error) {
	if path == "" {
		var err error
		path, err = defaultDBPath()
		if err != nil {
			return nil, fmt.Errorf("resolve default db path: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir data dir: %w", err)
	}

	writer, err := sql.Open("sqlite", "file:"+path+"?"+writerPragmas)
	if err != nil {
		return nil, fmt.Errorf("open writer: %w", err)
	}
	writer.SetMaxOpenConns(1) // single writer per ADR-014 pattern #3

	if _, err := writer.Exec(schemaSQL); err != nil {
		writer.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	reader, err := sql.Open("sqlite", "file:"+path+"?mode=ro&"+readerPragmas)
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("open reader: %w", err)
	}
	reader.SetMaxOpenConns(readerPoolSize)

	return &Store{writer: writer, reader: reader, path: path}, nil
}

// Path returns the resolved database file path.
func (s *Store) Path() string { return s.path }

// Close releases both pools. If both fail, errors are joined per
// cc-peer PR#1 minor #2 (don't silently swallow the second error).
func (s *Store) Close() error {
	return errors.Join(s.writer.Close(), s.reader.Close())
}

// Insert appends a single engram via the writer pool. Higher-throughput
// bulk paths should batch via prepared statement + single transaction
// per ADR-014 pattern #4 (out of Phase 1 scope; lands in capture layer).
func (s *Store) Insert(ctx context.Context, e engram.Engram) (int64, error) {
	if e.Surface == "" {
		return 0, errors.New("engram surface required")
	}
	if e.TS == 0 {
		return 0, errors.New("engram ts required")
	}
	if e.Payload == "" {
		// Schema declares payload TEXT NOT NULL but SQLite treats empty
		// string as satisfying NOT NULL. Enforce semantic-required at the
		// Go boundary per cc-peer PR#1 concern #1.
		return 0, errors.New("engram payload required")
	}
	if len(e.Payload) > MaxPayloadBytes {
		return 0, fmt.Errorf("engram payload %d bytes exceeds MaxPayloadBytes %d", len(e.Payload), MaxPayloadBytes)
	}
	res, err := s.writer.ExecContext(ctx,
		`INSERT INTO engrams (surface, ts, payload, meta) VALUES (?, ?, ?, ?)`,
		e.Surface, e.TS, e.Payload, e.Meta,
	)
	if err != nil {
		return 0, fmt.Errorf("insert engram: %w", err)
	}
	return res.LastInsertId()
}

// Retrieve runs the canonical lookup: surface + optional since (unix ns)
// + limit. Uses the read-only reader pool. Returns rows in (surface, ts DESC)
// order — covered by idx_surface_ts.
//
// Two-branch query path per cc-peer PR#1 concern #2 — the prior single-query
// shape `WHERE surface=? AND (?=0 OR ts>?)` worked but was fragile to refactor:
// dropping the `hasSince` flag would silently turn unfiltered queries into
// 0-row results.
func (s *Store) Retrieve(ctx context.Context, surface string, since int64, limit int) ([]engram.Engram, error) {
	if surface == "" {
		return nil, errors.New("surface required")
	}
	if limit <= 0 {
		limit = 50
	} else if limit > 500 {
		limit = 500
	}

	const baseSelect = `SELECT id, surface, ts, payload, COALESCE(meta, '') FROM engrams `
	const orderLimit = ` ORDER BY ts DESC LIMIT ?`

	var (
		rows *sql.Rows
		err  error
	)
	if since > 0 {
		rows, err = s.reader.QueryContext(ctx,
			baseSelect+`WHERE surface = ? AND ts > ?`+orderLimit,
			surface, since, limit,
		)
	} else {
		rows, err = s.reader.QueryContext(ctx,
			baseSelect+`WHERE surface = ?`+orderLimit,
			surface, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("retrieve query: %w", err)
	}
	defer rows.Close()

	out := make([]engram.Engram, 0, limit)
	for rows.Next() {
		var e engram.Engram
		if err := rows.Scan(&e.ID, &e.Surface, &e.TS, &e.Payload, &e.Meta); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row iter: %w", err)
	}
	return out, nil
}

// defaultDBPath resolves $EIDETIC_DATA_DIR or ~/.eidetic/engrams.db
// per spec § 2.2.
func defaultDBPath() (string, error) {
	if dir := os.Getenv("EIDETIC_DATA_DIR"); dir != "" {
		return filepath.Join(dir, "engrams.db"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".eidetic", "engrams.db"), nil
}
