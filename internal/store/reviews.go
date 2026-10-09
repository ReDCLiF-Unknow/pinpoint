package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxNameLen  = 60
	maxTitleLen = 200
)

// Review is one round of feedback on one site.
type Review struct {
	ID       int64
	Code     string // in the review URL
	FrameKey string // in the proxy's URLs
	SiteURL  string
	SiteHost string
	Title    string
	Reviewer string
	// AppliedAt is when the customer sent it; zero while it is a draft.
	AppliedAt time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
	// Count is how many annotations it has.
	Count int
}

func (r Review) Applied() bool { return !r.AppliedAt.IsZero() }

// clip trims s and cuts it to at most n characters.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}

// CreateReview starts a review of the site at siteURL, and returns it with the
// key for its edit link. Only a hash of the key is kept, so whoever has the
// link is the only one who can change the review.
func (s *Store) CreateReview(siteURL, host, title string) (Review, string, error) {
	if siteURL == "" || host == "" {
		return Review{}, "", ErrInvalid
	}
	key := newKey()
	r := Review{Code: newCode(), FrameKey: newCode(), SiteURL: siteURL, SiteHost: host, Title: clip(title, maxTitleLen)}
	res, err := s.db.Exec(`INSERT INTO reviews (code, edit_hash, frame_key, site_url, site_host, title) VALUES (?, ?, ?, ?, ?, ?)`,
		r.Code, hashKey(key), r.FrameKey, r.SiteURL, r.SiteHost, r.Title)
	if err != nil {
		return Review{}, "", err
	}
	r.ID, _ = res.LastInsertId()
	r, err = s.review(`r.id = ?`, r.ID)
	return r, key, err
}

const reviewCols = `r.id, r.code, r.frame_key, r.site_url, r.site_host, r.title, r.reviewer, r.applied_at, r.created_at, r.updated_at,
	(SELECT COUNT(*) FROM annotations a WHERE a.review_id = r.id)`

func (s *Store) review(where string, arg any) (Review, error) {
	var r Review
	var applied sql.NullTime
	err := s.db.QueryRow(`SELECT `+reviewCols+` FROM reviews r WHERE `+where, arg).Scan(
		&r.ID, &r.Code, &r.FrameKey, &r.SiteURL, &r.SiteHost, &r.Title, &r.Reviewer, &applied, &r.CreatedAt, &r.UpdatedAt, &r.Count)
	if errors.Is(err, sql.ErrNoRows) {
		return r, ErrNotFound
	}
	if applied.Valid {
		r.AppliedAt = applied.Time
	}
	return r, err
}

// ReviewByEditKey finds the review an edit link is for.
func (s *Store) ReviewByEditKey(key string) (Review, error) {
	if key == "" {
		return Review{}, ErrNotFound
	}
	return s.review(`r.edit_hash = ?`, hashKey(key))
}

// ReviewByCode finds the review a review URL is for.
func (s *Store) ReviewByCode(code string) (Review, error) {
	if code == "" {
		return Review{}, ErrNotFound
	}
	return s.review(`r.code = ?`, code)
}

// ReviewByFrameKey finds the review whose site the proxy is showing.
func (s *Store) ReviewByFrameKey(key string) (Review, error) {
	if key == "" {
		return Review{}, ErrNotFound
	}
	return s.review(`r.frame_key = ?`, key)
}

// Apply sends the review: from now on its URL shows it. The reviewer's name is
// optional. Applying again only updates the name, and keeps the first date.
func (s *Store) Apply(id int64, reviewer string) error {
	return affected(s.db.Exec(`UPDATE reviews SET reviewer = ?, applied_at = COALESCE(applied_at, CURRENT_TIMESTAMP),
		updated_at = CURRENT_TIMESTAMP WHERE id = ?`, clip(reviewer, maxNameLen), id))
}

func (s *Store) touch(id int64) {
	s.db.Exec(`UPDATE reviews SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, id)
}
