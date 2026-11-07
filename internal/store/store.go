// Package store is the History database: SQLite (modernc.org/sqlite, no cgo)
// in WAL mode, used by a single writer.
//
// History is organised in Families. A Family is one stored series (a fixed
// set of key columns plus value columns) kept at several Resolutions, finest
// first. Every value is a sum: byte counters are stored as byte deltas, and
// gauges are stored as a (sum, count) pair so that averaging stays exact
// through any number of rollups. That makes every rollup the same query:
// group the finer rows by the coarser slot (and the keys) and SUM every value.
//
// Each (Family, Resolution) is one table named <family>_<resolution> with
// columns (ts, keys..., values...), primary key (ts, keys...). Writes go to a
// Family's finest Resolution; coarser Resolutions are filled by the rollup in
// the same transaction once their slot is complete. Expiry and the guard cap
// (Maintain) apply to every table of every Family.
//
// Extension point: declare more Families and pass them to Open (the app's
// are sysFamily in internal/app/history.go and the device, endpoint and
// talkers Families in internal/app/devicehistory.go), then Write rows at the
// finest Resolution and Query them back (optionally matching leading keys);
// the rollup, retention and guard cap need no further code.
//
// Records (PutRecord, Records) are small keyed documents beside the
// Families, such as Device metadata; they are never expired or trimmed.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

// Resolution is one level of History: its slot length and how long its rows
// are kept.
type Resolution struct {
	Name string // "10s", "1m", "1h": also the table suffix
	Step time.Duration
	Keep time.Duration
}

// The Resolutions of spec 0001.
var (
	Res10s = Resolution{Name: "10s", Step: 10 * time.Second, Keep: 48 * time.Hour}
	Res1m  = Resolution{Name: "1m", Step: time.Minute, Keep: 30 * 24 * time.Hour}
	Res1h  = Resolution{Name: "1h", Step: time.Hour, Keep: 2 * 365 * 24 * time.Hour}
)

// Family is one stored series kept at several Resolutions (finest first; each
// Step must divide the next).
type Family struct {
	Name        string
	Keys        []string // TEXT key columns besides the slot time
	Values      []string // REAL sum columns
	Resolutions []Resolution
}

// Row is one slot of one Family. Keys and Values follow the Family's columns.
type Row struct {
	Slot   time.Time
	Keys   []string
	Values []float64
}

// Store is an open History database. It is safe for concurrent use. Writes
// and maintenance are serialised (one writer); Query runs on a separate pool
// of read connections, so a long read never holds up a write (WAL readers
// see the last committed snapshot).
type Store struct {
	path     string
	capBytes int64
	families []*Family

	rmu     sync.RWMutex // held for reading by every Query; Close takes it to close rdb
	rdb     *sql.DB
	rclosed bool

	size atomic.Int64

	mu     sync.Mutex
	db     *sql.DB
	closed bool
	ins    map[*Family]*sql.Stmt // prepared upsert into each Family's finest table
	marks  map[string]int64      // rollup watermarks (mirrors meta)
}

// Open opens (creating if needed) the database at path with the given
// Families. capBytes is the guard cap enforced by Maintain.
func Open(path string, capBytes int64, families ...*Family) (*Store, error) {
	// Connection pragmas go in the DSN so they hold for any new connection.
	dsn := "file:" + path + "?_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{path: path, capBytes: capBytes, families: families, db: db,
		ins: map[*Family]*sql.Stmt{}, marks: map[string]int64{}}
	if err := s.init(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	// Readers open after init, so the schema and WAL mode exist.
	rdb, err := sql.Open("sqlite", dsn+"&_pragma=query_only(1)")
	if err != nil {
		db.Close()
		return nil, err
	}
	rdb.SetMaxOpenConns(4)
	s.rdb = rdb
	s.size.Store(s.fileSize())
	return s, nil
}

func (s *Store) init() error {
	// auto_vacuum must be chosen before the first table exists; it lets
	// expiry and the guard cap give pages back to the file system.
	stmts := []string{
		"PRAGMA auto_vacuum=INCREMENTAL",
		"PRAGMA journal_mode=WAL",
		"CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v INTEGER NOT NULL)",
		"CREATE TABLE IF NOT EXISTS records (kind TEXT NOT NULL, k TEXT NOT NULL, v BLOB NOT NULL, PRIMARY KEY (kind, k)) WITHOUT ROWID",
	}
	for _, f := range s.families {
		for _, r := range f.Resolutions {
			cols := []string{"ts INTEGER NOT NULL"}
			pk := []string{"ts"}
			for _, k := range f.Keys {
				cols = append(cols, k+" TEXT NOT NULL")
				pk = append(pk, k)
			}
			for _, v := range f.Values {
				cols = append(cols, v+" REAL NOT NULL DEFAULT 0")
			}
			cols = append(cols, "PRIMARY KEY ("+strings.Join(pk, ", ")+")")
			stmts = append(stmts, fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (%s) WITHOUT ROWID",
				table(f, r), strings.Join(cols, ", ")))
		}
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	rows, err := s.db.Query("SELECT k, v FROM meta")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var v int64
		if err := rows.Scan(&k, &v); err != nil {
			return err
		}
		s.marks[k] = v
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, f := range s.families {
		fine := f.Resolutions[0]
		cols := append(append([]string{"ts"}, f.Keys...), f.Values...)
		sets := make([]string, len(f.Values))
		for i, v := range f.Values {
			sets[i] = fmt.Sprintf("%s = %s + excluded.%s", v, v, v)
		}
		conflict := append([]string{"ts"}, f.Keys...)
		st, err := s.db.Prepare(fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s",
			table(f, fine), strings.Join(cols, ", "), placeholders(len(cols)),
			strings.Join(conflict, ", "), strings.Join(sets, ", ")))
		if err != nil {
			return err
		}
		s.ins[f] = st
	}
	return nil
}

func table(f *Family, r Resolution) string { return f.Name + "_" + r.Name }

// Close flushes and closes the database. Later calls are no-ops.
func (s *Store) Close() error {
	s.rmu.Lock()
	if !s.rclosed {
		s.rclosed = true
		s.rdb.Close()
	}
	s.rmu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	for _, st := range s.ins {
		st.Close()
	}
	_, _ = s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return s.db.Close()
}

var errClosed = errors.New("store: closed")

// Write adds rows to f's finest Resolution in one transaction. A row whose
// slot and keys already exist is added to (so a slot written in parts, e.g.
// across a restart, sums up). Coarser Resolutions whose slots ended at or
// before now are rolled up in the same transaction.
func (s *Store) Write(f *Family, now time.Time, rows []Row) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errClosed
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	fine := f.Resolutions[0]
	ins := tx.Stmt(s.ins[f])
	defer ins.Close()
	for _, r := range rows {
		args := []any{slotMs(r.Slot, fine.Step)}
		for _, k := range r.Keys {
			args = append(args, k)
		}
		for _, v := range r.Values {
			args = append(args, v)
		}
		if _, err := ins.Exec(args...); err != nil {
			return err
		}
	}
	marks, err := rollup(tx, f, now, s.marks)
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for k, v := range marks {
		s.marks[k] = v
	}
	s.size.Store(s.fileSize())
	return nil
}

// rollup fills every coarser Resolution of f from the next finer one, for
// the coarse slots that ended at or before now and were not rolled up yet.
// A watermark per table (marks, persisted in meta) records how far it is
// done; it returns the advanced watermarks. Rows written later into an
// already rolled-up coarse slot are not rolled up again.
func rollup(tx *sql.Tx, f *Family, now time.Time, marks map[string]int64) (map[string]int64, error) {
	moved := map[string]int64{}
	for i := 1; i < len(f.Resolutions); i++ {
		src, dst := f.Resolutions[i-1], f.Resolutions[i]
		key := "rollup:" + table(f, dst)
		upTo := slotMs(now, dst.Step) // slots before this one are complete
		from, done := marks[key]
		if !done {
			from = -1 << 62
		}
		if from >= upTo {
			continue
		}
		step := dst.Step.Milliseconds()
		keys := ""
		if len(f.Keys) > 0 {
			keys = ", " + strings.Join(f.Keys, ", ")
		}
		sums := make([]string, len(f.Values))
		for j, v := range f.Values {
			sums[j] = "SUM(" + v + ")"
		}
		q := fmt.Sprintf(`INSERT OR REPLACE INTO %s (ts%s, %s)
			SELECT (ts / %d) * %d AS slot%s, %s FROM %s WHERE ts >= ? AND ts < ? GROUP BY slot%s`,
			table(f, dst), keys, strings.Join(f.Values, ", "),
			step, step, keys, strings.Join(sums, ", "), table(f, src), keys)
		if _, err := tx.Exec(q, from, upTo); err != nil {
			return nil, err
		}
		if _, err := tx.Exec("INSERT OR REPLACE INTO meta (k, v) VALUES (?, ?)", key, upTo); err != nil {
			return nil, err
		}
		moved[key] = upTo
	}
	return moved, nil
}

// Query returns f's rows at Resolution r with slots in [from, to), oldest
// first. match, if given, restricts the rows to those whose leading key
// columns equal it (e.g. one Device).
func (s *Store) Query(f *Family, r Resolution, from, to time.Time, match ...string) ([]Row, error) {
	s.rmu.RLock()
	defer s.rmu.RUnlock()
	if s.rclosed {
		return nil, errClosed
	}
	if len(match) > len(f.Keys) {
		return nil, fmt.Errorf("store: %d match keys for family %s", len(match), f.Name)
	}
	cols := append(append([]string{"ts"}, f.Keys...), f.Values...)
	where := "ts >= ? AND ts < ?"
	args := []any{from.UnixMilli(), to.UnixMilli()}
	for i, m := range match {
		where += " AND " + f.Keys[i] + " = ?"
		args = append(args, m)
	}
	rows, err := s.rdb.Query(fmt.Sprintf("SELECT %s FROM %s WHERE %s ORDER BY ts",
		strings.Join(cols, ", "), table(f, r), where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Row
	for rows.Next() {
		var ts int64
		row := Row{Keys: make([]string, len(f.Keys)), Values: make([]float64, len(f.Values))}
		dst := []any{&ts}
		for i := range row.Keys {
			dst = append(dst, &row.Keys[i])
		}
		for i := range row.Values {
			dst = append(dst, &row.Values[i])
		}
		if err := rows.Scan(dst...); err != nil {
			return nil, err
		}
		row.Slot = time.UnixMilli(ts)
		out = append(out, row)
	}
	return out, rows.Err()
}

// PutRecord stores value under (kind, key), replacing any previous value.
// Records are small keyed documents (e.g. Device metadata) kept outside the
// Families: they are not rolled up, expired or trimmed.
func (s *Store) PutRecord(kind, key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errClosed
	}
	_, err := s.db.Exec("INSERT OR REPLACE INTO records (kind, k, v) VALUES (?, ?, ?)", kind, key, value)
	return err
}

// Records returns every record of kind, by key.
func (s *Store) Records(kind string) (map[string][]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errClosed
	}
	rows, err := s.db.Query("SELECT k, v FROM records WHERE kind = ?", kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]byte{}
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// Maintain expires rows past their Resolution's retention, then enforces the
// guard cap by trimming the oldest rows (across every table) until the
// database fits.
func (s *Store) Maintain(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errClosed
	}
	for _, f := range s.families {
		for _, r := range f.Resolutions {
			if _, err := s.db.Exec("DELETE FROM "+table(f, r)+" WHERE ts < ?", now.Add(-r.Keep).UnixMilli()); err != nil {
				return err
			}
		}
	}
	if err := s.compact(); err != nil {
		return err
	}
	for s.size.Load() > s.capBytes {
		oldest, newest, ok, err := s.span()
		if err != nil {
			return err
		}
		if !ok {
			break // nothing left to trim
		}
		// Trim a sixteenth of the remaining span (at least one millisecond
		// past the oldest row) per round.
		cut := oldest + max((newest-oldest)/16, 1)
		for _, t := range s.tables() {
			if _, err := s.db.Exec("DELETE FROM "+t+" WHERE ts < ?", cut); err != nil {
				return err
			}
		}
		if err := s.compact(); err != nil {
			return err
		}
	}
	return nil
}

// compact returns free pages to the file system and folds the WAL into the
// main file so the file sizes reflect the live data.
func (s *Store) compact() error {
	// incremental_vacuum frees one page per step, so drain it as a query.
	rows, err := s.db.Query("PRAGMA incremental_vacuum")
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if _, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return err
	}
	s.size.Store(s.fileSize())
	return nil
}

func (s *Store) tables() []string {
	var out []string
	for _, f := range s.families {
		for _, r := range f.Resolutions {
			out = append(out, table(f, r))
		}
	}
	sort.Strings(out)
	return out
}

// span is the oldest and newest slot over every table.
func (s *Store) span() (oldest, newest int64, ok bool, err error) {
	for _, t := range s.tables() {
		var lo, hi sql.NullInt64
		if err := s.db.QueryRow("SELECT MIN(ts), MAX(ts) FROM "+t).Scan(&lo, &hi); err != nil {
			return 0, 0, false, err
		}
		if !lo.Valid {
			continue
		}
		if !ok || lo.Int64 < oldest {
			oldest = lo.Int64
		}
		if !ok || hi.Int64 > newest {
			newest = hi.Int64
		}
		ok = true
	}
	return oldest, newest, ok, nil
}

// Size is the database's size on disk in bytes (main file plus WAL), as of
// the last write or maintenance.
func (s *Store) Size() int64 { return s.size.Load() }

func (s *Store) fileSize() int64 {
	var n int64
	for _, p := range []string{s.path, s.path + "-wal"} {
		if fi, err := os.Stat(p); err == nil {
			n += fi.Size()
		}
	}
	return n
}

// Floor is the start of the step-long slot holding t, with slots aligned to
// the Unix epoch.
func Floor(t time.Time, step time.Duration) time.Time {
	return time.UnixMilli(slotMs(t, step))
}

func slotMs(t time.Time, step time.Duration) int64 {
	ms, st := t.UnixMilli(), step.Milliseconds()
	return ms - ((ms%st)+st)%st
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}
