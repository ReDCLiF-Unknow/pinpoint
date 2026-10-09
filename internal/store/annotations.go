package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"time"
)

const (
	maxCommentLen  = 4000
	maxSelectorLen = 1000
	maxElTextLen   = 300
	maxElHTMLLen   = 2000
	maxQuoteLen    = 1000
	maxGeometry    = 64 << 10
	// MaxShot is the largest screenshot kept. The page script scales them
	// down to fit well under it.
	MaxShot = 3 << 20
	// MaxAnnotations is how many one review can have.
	MaxAnnotations = 300
)

// Kinds are the ways to mark something on the page.
var Kinds = []string{"pin", "highlight", "box", "draw"}

// Labels are what an annotation asks for.
var Labels = []string{"change", "bug", "question", "like"}

func oneOf(s string, list []string) bool {
	for _, v := range list {
		if s == v {
			return true
		}
	}
	return false
}

// Annotation is one thing the customer marked on the page.
type Annotation struct {
	ID        int64   `json:"id"`
	Seq       int     `json:"seq"`
	Kind      string  `json:"kind"`
	Label     string  `json:"label"`
	Comment   string  `json:"comment"`
	PageURL   string  `json:"pageUrl"`
	PageTitle string  `json:"pageTitle"`
	Viewport  int     `json:"viewport"`
	Selector  string  `json:"selector"`
	ElTag     string  `json:"elTag"`
	ElText    string  `json:"elText"`
	ElHTML    string  `json:"elHtml"`
	Quote     string  `json:"quote"`
	PosX      float64 `json:"posX"`
	PosY      float64 `json:"posY"`
	// Geometry is the shape as the page script measured it. The server keeps
	// it as it came and hands it back; only the page script reads it.
	Geometry  json.RawMessage `json:"geometry"`
	HasShot   bool            `json:"hasShot"`
	CreatedAt time.Time       `json:"createdAt"`
}

// clean checks an annotation from a browser and trims it to size.
func (a *Annotation) clean() error {
	if !oneOf(a.Kind, Kinds) {
		return ErrInvalid
	}
	if a.Label == "" {
		a.Label = "change"
	}
	if !oneOf(a.Label, Labels) {
		return ErrInvalid
	}
	u, err := url.Parse(a.PageURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(a.PageURL) > 2000 {
		return ErrInvalid
	}
	if a.Viewport < 100 || a.Viewport > 10000 {
		return ErrInvalid
	}
	if len(a.Geometry) == 0 {
		a.Geometry = json.RawMessage("{}")
	}
	if len(a.Geometry) > maxGeometry || !json.Valid(a.Geometry) {
		return ErrInvalid
	}
	a.PosX, a.PosY = unit(a.PosX), unit(a.PosY)
	a.Comment = clip(a.Comment, maxCommentLen)
	a.PageTitle = clip(a.PageTitle, maxTitleLen)
	a.Selector = clip(a.Selector, maxSelectorLen)
	a.ElTag = clip(a.ElTag, 40)
	a.ElText = clip(a.ElText, maxElTextLen)
	a.ElHTML = clip(a.ElHTML, maxElHTMLLen)
	a.Quote = clip(a.Quote, maxQuoteLen)
	return nil
}

// unit keeps a position inside its element: between 0 and 1.
func unit(f float64) float64 {
	if f != f || f < 0 { // NaN, or before the start
		return 0
	}
	return min(f, 1)
}

// AddAnnotation adds a to the review and returns it as stored.
func (s *Store) AddAnnotation(reviewID int64, a Annotation) (Annotation, error) {
	if err := a.clean(); err != nil {
		return Annotation{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Annotation{}, err
	}
	defer tx.Rollback()
	var n, seq int
	if err := tx.QueryRow(`SELECT COUNT(*), COALESCE(MAX(seq), 0) FROM annotations WHERE review_id = ?`, reviewID).Scan(&n, &seq); err != nil {
		return Annotation{}, err
	}
	if n >= MaxAnnotations {
		return Annotation{}, ErrInvalid
	}
	res, err := tx.Exec(`INSERT INTO annotations (review_id, seq, kind, label, comment, page_url, page_title, viewport_w,
		selector, el_tag, el_text, el_html, quote, pos_x, pos_y, geometry) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		reviewID, seq+1, a.Kind, a.Label, a.Comment, a.PageURL, a.PageTitle, a.Viewport,
		a.Selector, a.ElTag, a.ElText, a.ElHTML, a.Quote, a.PosX, a.PosY, string(a.Geometry))
	if err != nil {
		return Annotation{}, err
	}
	id, _ := res.LastInsertId()
	if _, err := tx.Exec(`UPDATE reviews SET updated_at = CURRENT_TIMESTAMP WHERE id = ?`, reviewID); err != nil {
		return Annotation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Annotation{}, err
	}
	return s.annotation(reviewID, id)
}

const annotationCols = `id, seq, kind, label, comment, page_url, page_title, viewport_w, selector, el_tag, el_text, el_html,
	quote, pos_x, pos_y, geometry, shot IS NOT NULL, created_at`

func scanAnnotation(row interface{ Scan(...any) error }) (Annotation, error) {
	var a Annotation
	var geom string
	err := row.Scan(&a.ID, &a.Seq, &a.Kind, &a.Label, &a.Comment, &a.PageURL, &a.PageTitle, &a.Viewport, &a.Selector,
		&a.ElTag, &a.ElText, &a.ElHTML, &a.Quote, &a.PosX, &a.PosY, &geom, &a.HasShot, &a.CreatedAt)
	a.Geometry = json.RawMessage(geom)
	return a, err
}

func (s *Store) annotation(reviewID, id int64) (Annotation, error) {
	a, err := scanAnnotation(s.db.QueryRow(`SELECT `+annotationCols+` FROM annotations WHERE review_id = ? AND id = ?`, reviewID, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

// Annotations are a review's annotations in the order they were made.
func (s *Store) Annotations(reviewID int64) ([]Annotation, error) {
	rows, err := s.db.Query(`SELECT `+annotationCols+` FROM annotations WHERE review_id = ? ORDER BY seq`, reviewID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Annotation{}
	for rows.Next() {
		a, err := scanAnnotation(rows)
		if err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// UpdateAnnotation changes what an annotation says. The mark itself stays
// where it was drawn.
func (s *Store) UpdateAnnotation(reviewID, id int64, label, comment string) (Annotation, error) {
	if !oneOf(label, Labels) {
		return Annotation{}, ErrInvalid
	}
	err := affected(s.db.Exec(`UPDATE annotations SET label = ?, comment = ?, updated_at = CURRENT_TIMESTAMP
		WHERE review_id = ? AND id = ?`, label, clip(comment, maxCommentLen), reviewID, id))
	if err != nil {
		return Annotation{}, err
	}
	s.touch(reviewID)
	return s.annotation(reviewID, id)
}

// DeleteAnnotation removes an annotation and its screenshot.
func (s *Store) DeleteAnnotation(reviewID, id int64) error {
	if err := affected(s.db.Exec(`DELETE FROM annotations WHERE review_id = ? AND id = ?`, reviewID, id)); err != nil {
		return err
	}
	s.touch(reviewID)
	return nil
}

// SetShot stores the screenshot for an annotation, which the page takes while
// the customer is still typing their comment.
func (s *Store) SetShot(reviewID, id int64, png []byte) error {
	if len(png) == 0 || len(png) > MaxShot {
		return ErrInvalid
	}
	return affected(s.db.Exec(`UPDATE annotations SET shot = ? WHERE review_id = ? AND id = ?`, png, reviewID, id))
}

// Shot is the screenshot of the annotation with that seq.
func (s *Store) Shot(reviewID int64, seq int) ([]byte, error) {
	var png []byte
	err := s.db.QueryRow(`SELECT shot FROM annotations WHERE review_id = ? AND seq = ? AND shot IS NOT NULL`, reviewID, seq).Scan(&png)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return png, err
}
