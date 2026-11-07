package repocontext

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Cache persists parse results by content hash. A new base SHA normally changes
// a handful of files, so only those are re-parsed.
type Cache struct {
	db      *sql.DB
	maxRows int
}

// OpenCache creates the cache directory and database.
func OpenCache(dir string, maxRows int) (*Cache, error) {
	if dir == "" {
		dir = ".ekravo-context"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create context cache: %w", err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "index.db")+
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	c := &Cache{db: db, maxRows: maxRows}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	_, err = db.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS parse_cache (
  repo         TEXT NOT NULL,
  path         TEXT NOT NULL,
  content_hash TEXT NOT NULL,
  lang         TEXT NOT NULL,
  data         TEXT NOT NULL,
  parsed_at    TEXT NOT NULL,
  PRIMARY KEY (repo, path, content_hash)
);
CREATE INDEX IF NOT EXISTS idx_parse_cache_age ON parse_cache(parsed_at);
`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate context cache: %w", err)
	}
	return c, nil
}

// Close releases the cache handle.
func (c *Cache) Close() error { return c.db.Close() }

// Hash returns the cache key for a file body.
func Hash(src []byte) string {
	sum := sha256.Sum256(src)
	return hex.EncodeToString(sum[:])
}

// Get returns a cached parse result.
func (c *Cache) Get(ctx context.Context, repo, path, hash string) (*FileSymbols, bool) {
	var data string
	err := c.db.QueryRowContext(ctx,
		`SELECT data FROM parse_cache WHERE repo = ? AND path = ? AND content_hash = ?`,
		repo, path, hash).Scan(&data)
	if err != nil {
		return nil, false
	}
	var fs FileSymbols
	if json.Unmarshal([]byte(data), &fs) != nil {
		return nil, false
	}
	return &fs, true
}

// Put stores one parse result and trims the table when it grows too large.
func (c *Cache) Put(ctx context.Context, repo, path, hash string, fs *FileSymbols) error {
	data, err := json.Marshal(fs)
	if err != nil {
		return err
	}
	_, err = c.db.ExecContext(ctx, `
INSERT INTO parse_cache (repo, path, content_hash, lang, data, parsed_at)
VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(repo, path, content_hash) DO UPDATE SET
  data = excluded.data, lang = excluded.lang, parsed_at = excluded.parsed_at`,
		repo, path, hash, fs.Lang, string(data), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	return c.trim(ctx)
}

func (c *Cache) trim(ctx context.Context) error {
	if c.maxRows <= 0 {
		return nil
	}
	var n int
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM parse_cache`).Scan(&n); err != nil {
		return nil
	}
	if n <= c.maxRows {
		return nil
	}
	_, err := c.db.ExecContext(ctx, `
DELETE FROM parse_cache WHERE rowid IN (
  SELECT rowid FROM parse_cache ORDER BY parsed_at ASC LIMIT ?
)`, n-c.maxRows)
	return err
}
