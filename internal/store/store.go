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
const dsnPragmas = "_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=cache_size(-64000)"

// readerPoolSize covers 5 expected surfaces + headroom (per ADR-014 pattern #3).
const readerPoolSize = 8

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

	writer, err := sql.Open("sqlite", "file:"+path+"?"+dsnPragmas)
	if err != nil {
		return nil, fmt.Errorf("open writer: %w", err)
	}
	writer.SetMaxOpenConns(1) // single writer per ADR-014 pattern #3

	if _, err := writer.Exec(schemaSQL); err != nil {
		writer.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	reader, err := sql.Open("sqlite", "file:"+path+"?mode=ro&"+dsnPragmas)
	if err != nil {
		writer.Close()
		return nil, fmt.Errorf("open reader: %w", err)
	}
	reader.SetMaxOpenConns(readerPoolSize)

	return &Store{writer: writer, reader: reader, path: path}, nil
}

// Path returns the resolved database file path.
func (s *Store) Path() string { return s.path }

// Close releases both pools.
func (s *Store) Close() error {
	var first error
	if err := s.writer.Close(); err != nil {
		first = err
	}
	if err := s.reader.Close(); err != nil && first == nil {
		first = err
	}
	return first
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
func (s *Store) Retrieve(ctx context.Context, surface string, since int64, limit int) ([]engram.Engram, error) {
	if surface == "" {
		return nil, errors.New("surface required")
	}
	if limit <= 0 {
		limit = 50
	} else if limit > 500 {
		limit = 500
	}

	var (
		sinceArg interface{}
		hasSince int
	)
	if since > 0 {
		sinceArg = since
		hasSince = 1
	} else {
		sinceArg = nil
		hasSince = 0
	}

	rows, err := s.reader.QueryContext(ctx,
		`SELECT id, surface, ts, payload, COALESCE(meta, '')
		 FROM engrams
		 WHERE surface = ? AND (? = 0 OR ts > ?)
		 ORDER BY ts DESC
		 LIMIT ?`,
		surface, hasSince, sinceArg, limit,
	)
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
