package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"

	"pinpoint/internal/store"
)

// editorPage is the review editor: the site in a frame, the tools, and the
// list of what has been marked so far.
type editorPage struct {
	Review  store.Review
	Config  editorConfig
	Version string
}

// editorConfig is what the editor's script starts from.
type editorConfig struct {
	Mode        string             `json:"mode"` // "edit" or "view"
	API         string             `json:"api,omitempty"`
	Share       string             `json:"share"`
	Applied     bool               `json:"applied"`
	Reviewer    string             `json:"reviewer"`
	Title       string             `json:"title"`
	Annotations []store.Annotation `json:"annotations"`
	// Text is what the script translates its text with.
	Text map[string]string `json:"text,omitempty"`
	reviewFrame
}

func (s *Server) editReview(w http.ResponseWriter, r *http.Request) (store.Review, bool) {
	rv, err := s.store.ReviewByEditKey(r.PathValue("key"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			jsonError(w, r, http.StatusNotFound, "This edit link doesn't lead to a review.")
		} else {
			log.Print(err)
			jsonError(w, r, http.StatusInternalServerError, "Something went wrong.")
		}
		return rv, false
	}
	return rv, true
}

func (s *Server) editorPage(w http.ResponseWriter, r *http.Request) {
	rv, err := s.store.ReviewByEditKey(r.PathValue("key"))
	if err != nil {
		s.message(w, r, http.StatusNotFound, "This review isn't here",
			"The edit link may be incomplete. Check you copied all of it.")
		return
	}
	list, err := s.store.Annotations(rv.ID)
	if err != nil {
		log.Print(err)
		s.message(w, r, http.StatusInternalServerError, "Something went wrong", "Try again in a moment.")
		return
	}
	// The edit link is the only key to the review, so it must not leak in a
	// Referer to anything the page links to.
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	s.render(w, r, http.StatusOK, "editor.html", editorPage{
		Review:  rv,
		Version: s.assetVersion,
		Config: editorConfig{
			Mode:        "edit",
			API:         "/edit/" + r.PathValue("key"),
			Share:       baseURL(r) + "/r/" + rv.Code,
			Applied:     rv.Applied(),
			Reviewer:    rv.Reviewer,
			Title:       rv.Title,
			Annotations: list,
			Text:        langOf(r).text(),
			reviewFrame: newReviewFrame(r, rv),
		},
	})
}

// jsonError answers with msg, in English, translated for the request.
func jsonError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": langOf(r).T(msg)})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// readJSON decodes a request body of at most limit bytes into v.
func readJSON(w http.ResponseWriter, r *http.Request, limit int64, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v); err != nil {
		jsonError(w, r, http.StatusBadRequest, "That request didn't make sense.")
		return false
	}
	return true
}

func (s *Server) storeError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		jsonError(w, r, http.StatusNotFound, "That annotation is gone.")
	case errors.Is(err, store.ErrInvalid):
		jsonError(w, r, http.StatusUnprocessableEntity, "That annotation couldn't be saved.")
	default:
		log.Print(err)
		jsonError(w, r, http.StatusInternalServerError, "Something went wrong.")
	}
}

func (s *Server) apiAdd(w http.ResponseWriter, r *http.Request) {
	rv, ok := s.editReview(w, r)
	if !ok {
		return
	}
	var a store.Annotation
	if !readJSON(w, r, 256<<10, &a) {
		return
	}
	a, err := s.store.AddAnnotation(rv.ID, a)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func annotationID(r *http.Request) int64 {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id
}

func (s *Server) apiUpdate(w http.ResponseWriter, r *http.Request) {
	rv, ok := s.editReview(w, r)
	if !ok {
		return
	}
	var in struct{ Label, Comment string }
	if !readJSON(w, r, 64<<10, &in) {
		return
	}
	a, err := s.store.UpdateAnnotation(rv.ID, annotationID(r), in.Label, in.Comment)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) apiDelete(w http.ResponseWriter, r *http.Request) {
	rv, ok := s.editReview(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteAnnotation(rv.ID, annotationID(r)); err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

// apiShot stores the screenshot the page took, sent as the PNG itself.
func (s *Server) apiShot(w http.ResponseWriter, r *http.Request) {
	rv, ok := s.editReview(w, r)
	if !ok {
		return
	}
	png, err := io.ReadAll(http.MaxBytesReader(w, r.Body, store.MaxShot))
	if err != nil {
		jsonError(w, r, http.StatusRequestEntityTooLarge, "That screenshot is too large.")
		return
	}
	// Served back as image/png to whoever opens the review, so it had
	// better be one.
	if !bytes.HasPrefix(png, pngMagic) {
		jsonError(w, r, http.StatusUnsupportedMediaType, "A screenshot has to be a PNG.")
		return
	}
	if err := s.store.SetShot(rv.ID, annotationID(r), png); err != nil {
		s.storeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// apiApply sends the review: it gets its review URL, and the customer is
// shown it to pass on.
func (s *Server) apiApply(w http.ResponseWriter, r *http.Request) {
	rv, ok := s.editReview(w, r)
	if !ok {
		return
	}
	var in struct{ Name string }
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	if rv.Count == 0 {
		jsonError(w, r, http.StatusUnprocessableEntity, "Mark at least one thing on the page first.")
		return
	}
	if err := s.store.Apply(rv.ID, in.Name); err != nil {
		s.storeError(w, r, err)
		return
	}
	rv, _ = s.store.ReviewByCode(rv.Code)
	list, err := s.store.Annotations(rv.ID)
	if err != nil {
		s.storeError(w, r, err)
		return
	}
	share := baseURL(r) + "/r/" + rv.Code
	writeJSON(w, http.StatusOK, map[string]string{"url": share, "markdown": reviewMarkdown(rv, list, share)})
}
