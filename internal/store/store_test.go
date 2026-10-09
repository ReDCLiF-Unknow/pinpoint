package store

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newReview(t *testing.T, s *Store) (Review, string) {
	t.Helper()
	r, key, err := s.CreateReview("https://example.com/", "example.com", "Example")
	if err != nil {
		t.Fatal(err)
	}
	return r, key
}

func pin(comment string) Annotation {
	return Annotation{Kind: "pin", Label: "change", Comment: comment, PageURL: "https://example.com/",
		Viewport: 1440, Selector: "header .btn", ElTag: "a", ElText: "Learn more", PosX: 0.4, PosY: 0.5,
		Geometry: json.RawMessage(`{"x":10,"y":20}`)}
}

func TestEachLinkFindsTheReviewAndNothingElse(t *testing.T) {
	s := open(t)
	r, key := newReview(t, s)
	other, otherKey := newReview(t, s)

	if got, err := s.ReviewByEditKey(key); err != nil || got.ID != r.ID {
		t.Errorf("edit key: got %v, %v", got.ID, err)
	}
	if got, err := s.ReviewByCode(r.Code); err != nil || got.ID != r.ID {
		t.Errorf("code: got %v, %v", got.ID, err)
	}
	if got, err := s.ReviewByFrameKey(r.FrameKey); err != nil || got.ID != r.ID {
		t.Errorf("frame key: got %v, %v", got.ID, err)
	}
	if got, _ := s.ReviewByEditKey(otherKey); got.ID != other.ID {
		t.Error("the second review's key found the first")
	}
	// The share code and the frame key are not edit keys.
	for _, wrong := range []string{"", r.Code, r.FrameKey, key + "x"} {
		if _, err := s.ReviewByEditKey(wrong); !errors.Is(err, ErrNotFound) {
			t.Errorf("edit key %q opened a review", wrong)
		}
	}
	if r.Code == r.FrameKey || len(key) < 32 {
		t.Errorf("codes are too alike or too short: %q %q %q", r.Code, r.FrameKey, key)
	}
}

func TestApplyKeepsTheFirstDate(t *testing.T) {
	s := open(t)
	r, _ := newReview(t, s)
	if r.Applied() {
		t.Fatal("a new review counts as sent")
	}
	if err := s.Apply(r.ID, "  Anna  "); err != nil {
		t.Fatal(err)
	}
	r, _ = s.ReviewByCode(r.Code)
	if !r.Applied() || r.Reviewer != "Anna" {
		t.Fatalf("after apply: %+v", r)
	}
	first := r.AppliedAt
	if err := s.Apply(r.ID, "Anna B."); err != nil {
		t.Fatal(err)
	}
	r, _ = s.ReviewByCode(r.Code)
	if !r.AppliedAt.Equal(first) || r.Reviewer != "Anna B." {
		t.Errorf("applying again: %v %q", r.AppliedAt, r.Reviewer)
	}
}

func TestAnnotationsKeepTheirNumbers(t *testing.T) {
	s := open(t)
	r, _ := newReview(t, s)
	var ids []int64
	for _, c := range []string{"one", "two", "three"} {
		a, err := s.AddAnnotation(r.ID, pin(c))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, a.ID)
	}
	if err := s.DeleteAnnotation(r.ID, ids[1]); err != nil {
		t.Fatal(err)
	}
	a, err := s.AddAnnotation(r.ID, pin("four"))
	if err != nil {
		t.Fatal(err)
	}
	// A deleted number is never handed out again, so a screenshot link made
	// before the delete never shows a different annotation.
	if a.Seq != 4 {
		t.Errorf("the new one is number %d, want 4", a.Seq)
	}
	list, _ := s.Annotations(r.ID)
	var got []string
	for _, a := range list {
		got = append(got, a.Comment)
	}
	if strings.Join(got, ",") != "one,three,four" {
		t.Errorf("annotations: %v", got)
	}
	if string(list[0].Geometry) != `{"x":10,"y":20}` {
		t.Errorf("geometry came back as %s", list[0].Geometry)
	}
}

func TestAnnotationsAreChecked(t *testing.T) {
	s := open(t)
	r, _ := newReview(t, s)
	for name, change := range map[string]func(*Annotation){
		"kind":       func(a *Annotation) { a.Kind = "laser" },
		"label":      func(a *Annotation) { a.Label = "urgent" },
		"page":       func(a *Annotation) { a.PageURL = "javascript:alert(1)" },
		"no page":    func(a *Annotation) { a.PageURL = "" },
		"viewport":   func(a *Annotation) { a.Viewport = 0 },
		"bad shape":  func(a *Annotation) { a.Geometry = json.RawMessage(`{"x":`) },
		"huge shape": func(a *Annotation) { a.Geometry = json.RawMessage(`"` + strings.Repeat("a", maxGeometry) + `"`) },
	} {
		a := pin("x")
		change(&a)
		if _, err := s.AddAnnotation(r.ID, a); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
	// Long text is cut rather than refused: losing the end of a comment is
	// better than losing all of it.
	a := pin(strings.Repeat("é", maxCommentLen+50))
	a.PosX, a.PosY = 7, -1
	a.Label = ""
	got, err := s.AddAnnotation(r.ID, a)
	if err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(got.Comment)); n != maxCommentLen {
		t.Errorf("comment is %d characters", n)
	}
	if got.PosX != 1 || got.PosY != 0 || got.Label != "change" {
		t.Errorf("position %v,%v label %q", got.PosX, got.PosY, got.Label)
	}
}

func TestAnnotationsBelongToTheirReview(t *testing.T) {
	s := open(t)
	r, _ := newReview(t, s)
	other, _ := newReview(t, s)
	a, _ := s.AddAnnotation(r.ID, pin("mine"))
	if _, err := s.UpdateAnnotation(other.ID, a.ID, "bug", "hijacked"); !errors.Is(err, ErrNotFound) {
		t.Errorf("another review changed it: %v", err)
	}
	if err := s.DeleteAnnotation(other.ID, a.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("another review deleted it: %v", err)
	}
	if err := s.SetShot(other.ID, a.ID, []byte("png")); !errors.Is(err, ErrNotFound) {
		t.Errorf("another review set its screenshot: %v", err)
	}
	got, err := s.UpdateAnnotation(r.ID, a.ID, "bug", "now a bug")
	if err != nil || got.Label != "bug" || got.Comment != "now a bug" {
		t.Errorf("update: %+v %v", got, err)
	}
}

func TestShots(t *testing.T) {
	s := open(t)
	r, _ := newReview(t, s)
	a, _ := s.AddAnnotation(r.ID, pin("x"))
	if _, err := s.Shot(r.ID, a.Seq); !errors.Is(err, ErrNotFound) {
		t.Errorf("a screenshot before one was taken: %v", err)
	}
	if err := s.SetShot(r.ID, a.ID, make([]byte, MaxShot+1)); !errors.Is(err, ErrInvalid) {
		t.Errorf("an oversized screenshot: %v", err)
	}
	if err := s.SetShot(r.ID, a.ID, []byte("png")); err != nil {
		t.Fatal(err)
	}
	png, err := s.Shot(r.ID, a.Seq)
	if err != nil || string(png) != "png" {
		t.Errorf("got %q, %v", png, err)
	}
	list, _ := s.Annotations(r.ID)
	if !list[0].HasShot {
		t.Error("the list does not know it has a screenshot")
	}
}

func TestCountsAnnotations(t *testing.T) {
	s := open(t)
	r, _ := newReview(t, s)
	s.AddAnnotation(r.ID, pin("a"))
	s.AddAnnotation(r.ID, pin("b"))
	r, _ = s.ReviewByCode(r.Code)
	if r.Count != 2 {
		t.Errorf("count %d", r.Count)
	}
}
