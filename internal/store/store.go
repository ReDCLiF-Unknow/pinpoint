// Package store keeps reviews and their annotations in one SQLite file.
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid input")
)

type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS reviews (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	code       TEXT NOT NULL UNIQUE,           -- in the review URL, which is shared
	edit_hash  TEXT NOT NULL UNIQUE,           -- hash of the key in the customer's edit link
	frame_key  TEXT NOT NULL UNIQUE,           -- lets the proxy show this one site, and nothing else
	site_url   TEXT NOT NULL,                  -- where the review started
	site_host  TEXT NOT NULL,                  -- which site the proxy may show for it
	title      TEXT NOT NULL DEFAULT '',       -- the start page's title
	reviewer   TEXT NOT NULL DEFAULT '',       -- who wrote it, as they gave their name
	applied_at TIMESTAMP,                      -- when it was sent; NULL while it is a draft
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS annotations (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	review_id  INTEGER NOT NULL REFERENCES reviews(id) ON DELETE CASCADE,
	seq        INTEGER NOT NULL,               -- stable within the review, for screenshot links
	kind       TEXT NOT NULL,                  -- pin, highlight, box, draw
	label      TEXT NOT NULL,                  -- change, bug, question, like
	comment    TEXT NOT NULL DEFAULT '',
	page_url   TEXT NOT NULL,
	page_title TEXT NOT NULL DEFAULT '',
	viewport_w INTEGER NOT NULL,               -- the screen width it was made at
	selector   TEXT NOT NULL DEFAULT '',
	el_tag     TEXT NOT NULL DEFAULT '',
	el_text    TEXT NOT NULL DEFAULT '',
	el_html    TEXT NOT NULL DEFAULT '',
	quote      TEXT NOT NULL DEFAULT '',       -- the text a highlight covers
	pos_x      REAL NOT NULL DEFAULT 0,        -- where in the element, 0..1 across
	pos_y      REAL NOT NULL DEFAULT 0,        -- and 0..1 down
	geometry   TEXT NOT NULL DEFAULT '{}',     -- the shape, as the page script drew it
	shot       BLOB,                           -- PNG, cropped, with the annotation drawn on it
	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE (review_id, seq)
);
`

// Open opens (creating if needed) the SQLite database at path.
func Open(path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// An in-memory database is private to its connection, so a second one
	// would open an empty database: those stay on one.
	conns := 8
	if strings.Contains(path, ":memory:") || strings.Contains(path, "mode=memory") {
		conns = 1
	}
	db.SetMaxOpenConns(conns)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// newCode makes a link code: 16 characters, 96 random bits, which nobody
// guesses.
func newCode() string {
	b := make([]byte, 12)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// newKey makes a secret: 32 characters, 192 random bits.
func newKey() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func hashKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// affected turns "no rows changed" into ErrNotFound.
func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
