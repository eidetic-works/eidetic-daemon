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
	"sort"
	"strings"
	"time"

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
// busy_timeout + cache_size apply per PR#1 review minor #1.
const readerPragmas = "_pragma=busy_timeout(5000)&_pragma=cache_size(-64000)"

// readerPoolSize covers 5 expected surfaces + headroom (per ADR-014 pattern #3).
const readerPoolSize = 8

// MaxPayloadBytes caps Insert payload to prevent a single oversized engram
// (e.g., a 50MB Cursor JSONL chunk from a Phase-3 parser bug) from blocking
// ALL writers under the SetMaxOpenConns(1) writer-pool shape (PR#1 review
// concern #3).
//
// 8 MiB cap covers real Claude Code session-JSONL chunks measured during a
// 2026-05-13 runtime spike (largest observed: 2.41 MiB; 1 MiB original cap
// dropped 8+ engrams in the first 1s of capture). 8 MiB = ~3.3× over the
// measured ceiling — still bounded against the parser-bug failure mode.
// See ADR-017 (docs/DECISIONS.md).
const MaxPayloadBytes = 8 << 20

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

	st := &Store{writer: writer, reader: reader, path: path}
	if err := st.backfillFTS(context.Background()); err != nil {
		// Non-fatal: existing rows won't be searchable until next restart
		// that succeeds, but the daemon still works. Log is printed by caller.
		_ = err
	}
	return st, nil
}

// backfillFTS populates engrams_fts from the main table when the FTS index
// is empty but engrams exist. This handles the upgrade path: daemons that
// were running before v0.0.14 have rows in engrams but nothing in
// engrams_fts (the table was just created by schema migration). After
// backfill, the AFTER INSERT / AFTER DELETE triggers keep the index in sync.
//
// Idempotent: if FTS already has rows (normal startup), this is a fast
// two-count query and an early return.
func (s *Store) backfillFTS(ctx context.Context) error {
	var ftsCount, engramCount int64
	if err := s.writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM engrams_fts`).Scan(&ftsCount); err != nil {
		return fmt.Errorf("backfill fts count: %w", err)
	}
	if ftsCount > 0 {
		return nil // index already populated
	}
	if err := s.reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM engrams`).Scan(&engramCount); err != nil {
		return fmt.Errorf("backfill engram count: %w", err)
	}
	if engramCount == 0 {
		return nil // nothing to backfill
	}
	_, err := s.writer.ExecContext(ctx,
		`INSERT INTO engrams_fts(rowid, payload, surface) SELECT id, payload, surface FROM engrams`,
	)
	if err != nil {
		return fmt.Errorf("backfill fts insert: %w", err)
	}
	return nil
}

// Path returns the resolved database file path.
func (s *Store) Path() string { return s.path }

// Close releases both pools. If both fail, errors are joined per
// PR#1 review minor #2 (don't silently swallow the second error).
func (s *Store) Close() error {
	return errors.Join(s.writer.Close(), s.reader.Close())
}

// Insert appends a single engram via the writer pool. Higher-throughput
// bulk paths should batch via prepared statement + single transaction
// per ADR-014 pattern #4 — see InsertBatch.
func (s *Store) Insert(ctx context.Context, e engram.Engram) (int64, error) {
	if err := validateEngram(e); err != nil {
		return 0, fmt.Errorf("%w: %w", ErrInvalidEngram, err)
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

// InsertBatch wraps a slice of inserts in a single transaction with a shared
// prepared statement (ADR-014 pattern #4). Required by the Phase-3 capture
// layer where one fsnotify file-event-batch produces N engrams that must
// land atomically (or roll back together if mid-batch validation fails).
//
// Caller is responsible for chunking very large batches; we apply
// MaxPayloadBytes per row and rely on SQLite's per-tx limit otherwise.
// Empty batch is a no-op (returns nil without opening a transaction).
func (s *Store) InsertBatch(ctx context.Context, batch []engram.Engram) error {
	if len(batch) == 0 {
		return nil
	}
	for i, e := range batch {
		if err := validateEngram(e); err != nil {
			return fmt.Errorf("batch[%d]: %w: %w", i, ErrInvalidEngram, err)
		}
	}
	tx, err := s.writer.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin batch tx: %w", err)
	}
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO engrams (surface, ts, payload, meta) VALUES (?, ?, ?, ?)`,
	)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("prepare batch stmt: %w", err)
	}
	defer stmt.Close()
	for i, e := range batch {
		if _, err := stmt.ExecContext(ctx, e.Surface, e.TS, e.Payload, e.Meta); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("batch[%d] exec: %w", i, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit batch tx: %w", err)
	}
	return nil
}

// validateEngram enforces semantic-required at the Go boundary (PR#1 review
// concern #1) + payload size cap (PR#1 review concern #3).
// Centralized so Insert + InsertBatch can't drift.
func validateEngram(e engram.Engram) error {
	if e.Surface == "" {
		return errors.New("engram surface required")
	}
	if e.TS == 0 {
		return errors.New("engram ts required")
	}
	if e.Payload == "" {
		return errors.New("engram payload required")
	}
	if len(e.Payload) > MaxPayloadBytes {
		return fmt.Errorf("engram payload %d bytes exceeds MaxPayloadBytes %d",
			len(e.Payload), MaxPayloadBytes)
	}
	return nil
}

// Retrieve runs the canonical lookup with optional filters. surface="" returns
// engrams across all surfaces (uses idx_ts; surface!="" uses idx_surface_ts).
// since and before are unix epoch nanoseconds; zero means no bound.
// asc=false returns newest-first (default); asc=true returns oldest-first.
// limit defaults to 50, clamped to 500. Uses the read-only reader pool.
func (s *Store) Retrieve(ctx context.Context, surface string, since, before int64, limit int, asc bool) ([]engram.Engram, error) {
	if limit <= 0 {
		limit = 50
	} else if limit > 500 {
		limit = 500
	}

	const baseSelect = `SELECT id, surface, ts, payload, COALESCE(meta, '') FROM engrams`
	order := ` ORDER BY ts DESC LIMIT ?`
	if asc {
		order = ` ORDER BY ts ASC LIMIT ?`
	}

	var clauses []string
	var args []interface{}
	if surface != "" {
		clauses = append(clauses, "surface = ?")
		args = append(args, surface)
	}
	if since > 0 {
		clauses = append(clauses, "ts > ?")
		args = append(args, since)
	}
	if before > 0 {
		clauses = append(clauses, "ts < ?")
		args = append(args, before)
	}
	args = append(args, limit)

	q := baseSelect
	if len(clauses) > 0 {
		q += " WHERE " + strings.Join(clauses, " AND ")
	}
	q += order

	rows, err := s.reader.QueryContext(ctx, q, args...)
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

// ExplainQuery returns SQLite EXPLAIN QUERY PLAN output for the canonical
// Retrieve shape. Test helper (used by store_test.go to assert idx_surface_ts
// is hit so a future schema change can't silently degrade the hot path to
// a table scan). Not part of the runtime API surface.
func (s *Store) ExplainQuery(ctx context.Context) (string, error) {
	rows, err := s.reader.QueryContext(ctx,
		`EXPLAIN QUERY PLAN
		 SELECT id, surface, ts, payload, meta
		 FROM engrams WHERE surface = ? ORDER BY ts DESC LIMIT ?`,
		"any", 50,
	)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var out string
	for rows.Next() {
		var id, parent, notUsed int
		var line string
		if err := rows.Scan(&id, &parent, &notUsed, &line); err != nil {
			return "", err
		}
		out += line + "\n"
	}
	return out, rows.Err()
}

// Count returns the total engram count across all surfaces. Reader-pool
// query — does not block writers. Used by the /metrics endpoint (v0.0.7+).
func (s *Store) Count(ctx context.Context) (int64, error) {
	var n int64
	err := s.reader.QueryRowContext(ctx, `SELECT COUNT(*) FROM engrams`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

// GetByID fetches a single engram by primary key. Uses the reader pool.
// Returns ErrNotFound when id has no matching row.
func (s *Store) GetByID(ctx context.Context, id int64) (engram.Engram, error) {
	row := s.reader.QueryRowContext(ctx,
		`SELECT id, surface, ts, payload, COALESCE(meta, '') FROM engrams WHERE id = ?`, id)
	var e engram.Engram
	if err := row.Scan(&e.ID, &e.Surface, &e.TS, &e.Payload, &e.Meta); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return engram.Engram{}, ErrNotFound
		}
		return engram.Engram{}, fmt.Errorf("get by id: %w", err)
	}
	return e, nil
}

// DeleteByID removes the engram with the given primary key. Returns
// ErrNotFound when no row with that id exists. Runs on the writer pool so
// WAL flush is consistent with other writes.
func (s *Store) DeleteByID(ctx context.Context, id int64) error {
	res, err := s.writer.ExecContext(ctx, `DELETE FROM engrams WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete by id: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountEngrams returns the number of engrams matching the given filters.
// surface="" counts across all surfaces. since=0 includes all timestamps.
// Reader-pool query — does not block writers. Used by GET /engrams/count (v0.0.20+).
func (s *Store) CountEngrams(ctx context.Context, surface string, since int64) (int64, error) {
	var (
		query string
		args  []any
	)
	switch {
	case surface != "" && since > 0:
		query = `SELECT COUNT(*) FROM engrams WHERE surface = ? AND ts > ?`
		args = []any{surface, since}
	case surface != "":
		query = `SELECT COUNT(*) FROM engrams WHERE surface = ?`
		args = []any{surface}
	case since > 0:
		query = `SELECT COUNT(*) FROM engrams WHERE ts > ?`
		args = []any{since}
	default:
		query = `SELECT COUNT(*) FROM engrams`
	}
	row := s.reader.QueryRowContext(ctx, query, args...)
	var n int64
	if err := row.Scan(&n); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	return n, nil
}

// CountBySurface returns engram count grouped by surface. Reader-pool
// query — does not block writers. Used by the /metrics endpoint to
// surface per-surface ingest visibility.
func (s *Store) CountBySurface(ctx context.Context) (map[string]int64, error) {
	rows, err := s.reader.QueryContext(ctx, `SELECT surface, COUNT(*) FROM engrams GROUP BY surface`)
	if err != nil {
		return nil, fmt.Errorf("count by surface query: %w", err)
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var (
			surface string
			n       int64
		)
		if err := rows.Scan(&surface, &n); err != nil {
			return nil, fmt.Errorf("count by surface scan: %w", err)
		}
		out[surface] = n
	}
	return out, rows.Err()
}

// Purge deletes engrams for a surface. When before > 0, only rows with
// ts < before are deleted (unix nanoseconds, matching the ts column). When
// before == 0, all rows for the surface are deleted. Returns the number of
// rows deleted. Writer-pool exec — does not block readers.
func (s *Store) Purge(ctx context.Context, surface string, before int64) (int64, error) {
	if surface == "" {
		return 0, errors.New("surface required")
	}
	var (
		res sql.Result
		err error
	)
	if before > 0 {
		res, err = s.writer.ExecContext(ctx,
			`DELETE FROM engrams WHERE surface = ? AND ts < ?`,
			surface, before,
		)
	} else {
		res, err = s.writer.ExecContext(ctx,
			`DELETE FROM engrams WHERE surface = ?`,
			surface,
		)
	}
	if err != nil {
		return 0, fmt.Errorf("purge: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("purge rows affected: %w", err)
	}
	return n, nil
}

// Search runs a full-text search over engram payloads using the FTS5 index.
// query is an FTS5 match expression (bare keywords work; phrase queries use
// double quotes e.g. `"benchmark result"`). surface is optional — when
// non-empty, only engrams from that surface are returned. limit follows the
// same clamping as Retrieve (default 50, max 500). Results are ordered by
// FTS5 relevance rank (best match first).
//
// Returns ErrEmptyQuery when q is empty so callers can return 400 rather
// than 500.
func (s *Store) Search(ctx context.Context, q, surface string, limit int) ([]engram.Engram, error) {
	if q == "" {
		return nil, ErrEmptyQuery
	}
	if limit <= 0 {
		limit = 50
	} else if limit > 500 {
		limit = 500
	}

	// snippet(engrams_fts, 0, ...) extracts a ~20-token context window from
	// column 0 (payload) around the FTS5 match. No highlight markers — plain
	// text with '...' ellipsis. This keeps MCP responses readable instead of
	// dumping 10KB raw JSON blobs at the AI.
	const base = `
		SELECT e.id, e.surface, e.ts, e.payload, COALESCE(e.meta, ''),
		       snippet(engrams_fts, 0, '', '', '...', 20)
		FROM engrams_fts
		JOIN engrams e ON e.id = engrams_fts.rowid
		WHERE engrams_fts MATCH ?`

	var (
		rows *sql.Rows
		err  error
	)
	if surface != "" {
		rows, err = s.reader.QueryContext(ctx,
			base+` AND engrams_fts.surface = ? ORDER BY rank LIMIT ?`,
			q, surface, limit,
		)
	} else {
		rows, err = s.reader.QueryContext(ctx,
			base+` ORDER BY rank LIMIT ?`,
			q, limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("search query: %w", err)
	}
	defer rows.Close()

	out := make([]engram.Engram, 0, limit)
	for rows.Next() {
		var e engram.Engram
		if err := rows.Scan(&e.ID, &e.Surface, &e.TS, &e.Payload, &e.Meta, &e.Snippet); err != nil {
			return nil, fmt.Errorf("search scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search row iter: %w", err)
	}
	return out, nil
}

// ErrEmptyQuery is returned by Search when the query string is empty.
var ErrEmptyQuery = errors.New("search query required")

// ErrInvalidEngram wraps validation failures from Insert/InsertBatch so HTTP
// handlers can map them to 400 rather than 500.
var ErrInvalidEngram = errors.New("invalid engram")

// ErrNotFound is returned by GetByID when no row matches the given id.
var ErrNotFound = errors.New("engram not found")

// Recent returns the N most recent engrams across ALL surfaces, ordered by
// ts DESC. When since > 0 only engrams with ts > since are returned (unix ns).
// limit follows the same clamping as Retrieve (default 50, max 500). Uses the
// reader pool — does not block writers.
//
// This is the "what have I been doing?" cross-surface query; callers that
// need surface isolation should use Retrieve instead.
func (s *Store) Recent(ctx context.Context, since, before int64, limit int) ([]engram.Engram, error) {
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
	switch {
	case since > 0 && before > 0:
		rows, err = s.reader.QueryContext(ctx,
			baseSelect+`WHERE ts > ? AND ts < ?`+orderLimit,
			since, before, limit,
		)
	case since > 0:
		rows, err = s.reader.QueryContext(ctx,
			baseSelect+`WHERE ts > ?`+orderLimit,
			since, limit,
		)
	case before > 0:
		rows, err = s.reader.QueryContext(ctx,
			baseSelect+`WHERE ts < ?`+orderLimit,
			before, limit,
		)
	default:
		rows, err = s.reader.QueryContext(ctx,
			baseSelect+orderLimit,
			limit,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("recent query: %w", err)
	}
	defer rows.Close()

	out := make([]engram.Engram, 0, limit)
	for rows.Next() {
		var e engram.Engram
		if err := rows.Scan(&e.ID, &e.Surface, &e.TS, &e.Payload, &e.Meta); err != nil {
			return nil, fmt.Errorf("recent scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("recent row iter: %w", err)
	}
	return out, nil
}

// StatsSnapshot holds a point-in-time summary of the engram database.
type StatsSnapshot struct {
	Total      int64            // total row count
	BySurface  map[string]int64 // per-surface counts
	OldestNs   int64            // min ts (unix nanoseconds), 0 if empty
	NewestNs   int64            // max ts (unix nanoseconds), 0 if empty
	DBBytes    int64            // file size on disk
	P95LatNs   int64            // P95 latency of a single-row read (nanoseconds)
}

// Stats returns a point-in-time summary. Opens the DB file for size; all
// queries run on the read pool. Safe to call while the daemon is running.
func (s *Store) Stats(ctx context.Context) (StatsSnapshot, error) {
	var snap StatsSnapshot

	total, err := s.Count(ctx)
	if err != nil {
		return snap, err
	}
	snap.Total = total

	by, err := s.CountBySurface(ctx)
	if err != nil {
		return snap, err
	}
	snap.BySurface = by

	row := s.reader.QueryRowContext(ctx, `SELECT MIN(ts), MAX(ts) FROM engrams`)
	var minTS, maxTS sql.NullInt64
	if err := row.Scan(&minTS, &maxTS); err != nil {
		return snap, fmt.Errorf("stats time range: %w", err)
	}
	if minTS.Valid {
		snap.OldestNs = minTS.Int64
	}
	if maxTS.Valid {
		snap.NewestNs = maxTS.Int64
	}

	if fi, err := os.Stat(s.path); err == nil {
		snap.DBBytes = fi.Size()
	}

	// P95 latency: 20 timed GetByID probes across the rowid range.
	if total > 0 {
		step := total / 20
		if step < 1 {
			step = 1
		}
		var samples []int64
		for i := int64(1); i <= total; i += step {
			t0 := time.Now()
			_, _ = s.GetByID(ctx, i)
			samples = append(samples, time.Since(t0).Nanoseconds())
		}
		if len(samples) > 0 {
			sort.Slice(samples, func(a, b int) bool { return samples[a] < samples[b] })
			p95idx := int(float64(len(samples)) * 0.95)
			if p95idx >= len(samples) {
				p95idx = len(samples) - 1
			}
			snap.P95LatNs = samples[p95idx]
		}
	}

	return snap, nil
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
