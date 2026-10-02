// Package web serves the phonehome dashboard and its JSON API.
package web

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/bizpers11991-code/phonehome/internal/model"
)

// Backend is everything the dashboard needs from the rest of phonehome.
type Backend interface {
	Report(ctx context.Context, p model.Period) (model.HomeReport, error)
	Status(ctx context.Context) (model.Status, error)
	SetLabel(ctx context.Context, deviceID, label string) error
	// Receipt renders the home receipt (deviceID == "") or one device's.
	// format is "svg" or "png".
	Receipt(ctx context.Context, p model.Period, deviceID, format string) ([]byte, error)
}

// ErrNotFound may be returned (or wrapped) by a Backend when a device does
// not exist; the API then answers 404 instead of 500.
var ErrNotFound = errors.New("not found")

// Options configures the handler. The zero value is usable.
type Options struct {
	// Username and Password enable HTTP basic auth when Password is set.
	Username, Password string
	Now                func() time.Time
	Logger             *slog.Logger
}

// MaxLabelLen is the longest device label accepted, in characters.
const MaxLabelLen = 64

type server struct {
	b      Backend
	now    func() time.Time
	log    *slog.Logger
	assets assets
}

// New returns the dashboard and API as one http.Handler.
func New(b Backend, o Options) http.Handler {
	s := &server{b: b, now: o.Now, log: o.Logger, assets: loadAssets()}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = slog.Default()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.assets.serve("index.html"))
	mux.HandleFunc("GET /assets/{name}", func(w http.ResponseWriter, r *http.Request) {
		s.assets.serve(r.PathValue("name"))(w, r)
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("GET /api/report", s.report)
	mux.HandleFunc("GET /api/status", s.status)
	mux.HandleFunc("POST /api/devices/{id}/label", s.setLabel)
	mux.HandleFunc("GET /receipt/{file}", s.receipt)

	var h http.Handler = mux
	if o.Password != "" {
		h = basicAuth(h, o.Username, o.Password)
	}
	return s.logRequests(securityHeaders(h))
}

// period parses ?days=1|7|30 (default 7) into [now−days, now).
func (s *server) period(r *http.Request) (model.Period, bool) {
	days := 7
	if v := r.URL.Query().Get("days"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || (n != 1 && n != 7 && n != 30) {
			return model.Period{}, false
		}
		days = n
	}
	now := s.now()
	return model.Period{From: now.AddDate(0, 0, -days), To: now}, true
}

func (s *server) report(w http.ResponseWriter, r *http.Request) {
	p, ok := s.period(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "days must be 1, 7 or 30")
		return
	}
	rep, err := s.b.Report(r.Context(), p)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newReportDTO(rep))
}

func (s *server) status(w http.ResponseWriter, r *http.Request) {
	st, err := s.b.Status(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newStatusDTO(st, s.now()))
}

func (s *server) setLabel(w http.ResponseWriter, r *http.Request) {
	if err := checkCSRF(r); err != nil {
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	var body struct {
		Label *string `json:"label"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil || body.Label == nil {
		writeError(w, http.StatusBadRequest, `body must be {"label": "..."}`)
		return
	}
	label, err := cleanLabel(*body.Label)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.b.SetLabel(r.Context(), r.PathValue("id"), label); err != nil {
		s.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// cleanLabel trims a label and rejects ones that are too long or contain
// control characters. An empty label clears it.
func cleanLabel(s string) (string, error) {
	s = strings.TrimSpace(s)
	switch {
	case !utf8.ValidString(s):
		return "", errors.New("label must be valid UTF-8")
	case utf8.RuneCountInString(s) > MaxLabelLen:
		return "", errors.New("label must be at most 64 characters")
	case strings.ContainsFunc(s, unicode.IsControl):
		return "", errors.New("label must not contain control characters")
	}
	return s, nil
}

var crossOrigin = http.NewCrossOriginProtection()

// checkCSRF requires a JSON body (which a plain HTML form cannot send) and a
// same-origin request, judged by Sec-Fetch-Site or Origin versus Host.
func checkCSRF(r *http.Request) error {
	mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mt != "application/json" {
		return errors.New("content type must be application/json")
	}
	if err := crossOrigin.Check(r); err != nil {
		return errors.New("cross-origin request refused")
	}
	return nil
}

func (s *server) receipt(w http.ResponseWriter, r *http.Request) {
	// Split on the last dot: IDs such as "ip:192.168.1.5" contain dots.
	file := r.PathValue("file")
	i := strings.LastIndexByte(file, '.')
	id, format := file[:max(i, 0)], file[i+1:]
	ctype := map[string]string{"svg": "image/svg+xml", "png": "image/png"}[format]
	if i <= 0 || ctype == "" {
		writeError(w, http.StatusNotFound, "receipts are /receipt/home.svg or /receipt/{device}.png")
		return
	}
	if id == "home" {
		id = ""
	}
	p, ok := s.period(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "days must be 1, 7 or 30")
		return
	}
	img, err := s.b.Receipt(r.Context(), p, id, format)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-store")
	w.Write(img)
}

// fail reports a backend error without leaking its details to the client.
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such device")
		return
	}
	if r.Context().Err() == nil {
		s.log.ErrorContext(r.Context(), "backend error", "path", r.URL.Path, "err", err)
	}
	writeError(w, http.StatusInternalServerError, "something went wrong; see the phonehome log")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
