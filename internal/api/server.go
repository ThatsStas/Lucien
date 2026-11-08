// Package api is the HTTP layer (DESIGN.md 5): routing, bearer auth,
// request parsing and the mapping from Note Store sentinels to status
// codes. All state lives behind Store and Index; handlers hold none.
package api

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"lucien/internal/config"
	"lucien/internal/index"
	"lucien/internal/notestore"
)

const (
	maxJSONBody  = 1 << 20 // 1 MB: DESIGN.md is silent, DoD-flagged choice
	defaultLimit = 100
	maxLimit     = 1000
)

// Server bundles the dependencies the handlers need and builds the mux.
type Server struct {
	store     *notestore.Store
	idx       *index.Index
	clients   []config.Client
	maxUpload int64
	log       *slog.Logger
}

func New(store *notestore.Store, idx *index.Index, cfg *config.Config, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		store:     store,
		idx:       idx,
		clients:   cfg.Clients,
		maxUpload: int64(cfg.MaxUploadMB) << 20,
		log:       log,
	}
}

// Handler returns the authenticated mux. /v1/health is the only route
// outside auth (DESIGN.md 5).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	auth := s.requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/v1/notes":
			s.handleCreate(w, r)
		case r.Method == "GET" && r.URL.Path == "/v1/notes":
			s.handleList(w, r)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/append") && strings.HasPrefix(r.URL.Path, "/v1/notes/"):
			s.handleAppend(w, r, strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/notes/"), "/append"))
		case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/v1/notes/"):
			s.handleGet(w, r, strings.TrimPrefix(r.URL.Path, "/v1/notes/"))
		case r.Method == "PATCH" && strings.HasPrefix(r.URL.Path, "/v1/notes/"):
			s.handlePatch(w, r, strings.TrimPrefix(r.URL.Path, "/v1/notes/"))
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/v1/notes/"):
			s.handleDelete(w, r, strings.TrimPrefix(r.URL.Path, "/v1/notes/"))
		case r.Method == "POST" && r.URL.Path == "/v1/admin/reindex":
			s.handleReindex(w, r)
		default:
			jsonErr(w, http.StatusNotFound, "not found")
		}
	}))
	mux.Handle("/v1/", auth)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		jsonErr(w, http.StatusNotFound, "not found")
	})
	return s.withAccessLog(mux)
}

// --- access log ----------------------------------------------------------------

// responseMeta collects what the handlers decided about a response so the
// access line can state it: final status and, for errors, the message the
// client saw. jsonOut/jsonErr are the choke points that fill it in.
type responseMeta struct {
	status int
	reason string
}

// statusWriter captures the first WriteHeader code; Unwrap keeps
// http.ResponseController working through the wrapper.
type statusWriter struct {
	http.ResponseWriter
	meta *responseMeta
}

func (sw *statusWriter) WriteHeader(code int) {
	if sw.meta.status == 0 {
		sw.meta.status = code
	}
	sw.ResponseWriter.WriteHeader(code)
}

func (sw *statusWriter) Unwrap() http.ResponseWriter { return sw.ResponseWriter }

func metaOf(w http.ResponseWriter) *responseMeta {
	if sw, ok := w.(*statusWriter); ok {
		return sw.meta
	}
	return nil
}

// withAccessLog emits exactly one debug line per HTTP request: method,
// path, status, duration, remote and the server's reason when the request
// bounced. Mechanics only — invisible at the default level.
func (s *Server) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, meta: &responseMeta{}}
		next.ServeHTTP(sw, r)
		status := sw.meta.status
		if status == 0 {
			status = http.StatusOK
		}
		s.log.Debug("http request",
			"method", r.Method, "path", r.URL.Path,
			"status", status, "dur_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr, "reason", sw.meta.reason)
	})
}

// --- auth ---------------------------------------------------------------------

// requireAuth bounces with a WARN naming the FAILURE MODE (missing vs
// malformed vs unknown token) plus request context — the story an operator
// needs when a device "keeps getting 401". The presented credential is
// never logged, right or wrong. Accepted requests record which client
// authenticated, at debug.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		const prefix = "Bearer "
		var reason, client string
		switch {
		case h == "":
			reason = "missing_header"
		case !strings.HasPrefix(h, prefix):
			reason = "malformed_header"
		default:
			if client = s.matchClient(strings.TrimPrefix(h, prefix)); client == "" {
				reason = "unknown_token"
			}
		}
		if reason != "" {
			s.log.Warn("auth rejected", "reason", reason,
				"method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
			jsonErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		s.log.Debug("auth accepted", "client", client,
			"method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
		next.ServeHTTP(w, r)
	})
}

// matchClient hashes the candidate and constant-time compares against every
// configured client hash (DESIGN.md 5, 12), returning the client name.
// Comparison cost is identical for every client: no timing signal about
// which one matched. "" means no client matched.
func (s *Server) matchClient(token string) string {
	if token == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(token))
	for _, c := range s.clients {
		want, err := hex.DecodeString(c.TokenSHA256)
		if err != nil || len(want) != len(sum) {
			continue
		}
		if subtle.ConstantTimeCompare(sum[:], want) == 1 {
			return c.Name
		}
	}
	return ""
}

// --- handlers -----------------------------------------------------------------

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, http.StatusOK, map[string]string{"status": "ok"})
}

// POST /v1/notes (DESIGN.md 5.1). Phase 1 kinds: text (JSON) and audio
// (multipart); url/link is phase 2 and rejected at parse.
func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "multipart/form-data"):
		s.createAudio(w, r)
	case strings.HasPrefix(ct, "application/json"):
		s.createJSON(w, r)
	default:
		jsonErr(w, http.StatusUnsupportedMediaType, "application/json or multipart/form-data required")
	}
}

type createJSONReq struct {
	Text       *string          `json:"text"`
	Tags       *json.RawMessage `json:"tags"`
	State      *string          `json:"state"`
	Priority   *int             `json:"priority"`
	RemindAt   *string          `json:"remind_at"`
	DeferUntil *string          `json:"defer_until"`
	URL        *string          `json:"url"` // phase 2, rejected below
}

func (s *Server) createJSON(w http.ResponseWriter, r *http.Request) {
	var req createJSONReq
	if err := decodeJSON(r, &req); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.URL != nil {
		jsonErr(w, http.StatusBadRequest, "url notes arrive with the link pipeline (phase 2)")
		return
	}
	if req.Text == nil || strings.TrimSpace(*req.Text) == "" {
		jsonErr(w, http.StatusBadRequest, "text is required")
		return
	}
	tags, err := parseTags(req.Tags)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cReq := notestore.CreateRequest{SourceKind: "text", Text: *req.Text, Tags: tags}
	if req.State != nil {
		cReq.State = *req.State
	}
	if req.Priority != nil {
		cReq.Priority = *req.Priority
	}
	if err := parseTimes(req.RemindAt, req.DeferUntil, &cReq.RemindAt, &cReq.DeferUntil); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := s.store.Create(cReq)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	jsonOut(w, http.StatusCreated, map[string]string{"id": n.ID})
}

func (s *Server) createAudio(w http.ResponseWriter, r *http.Request) {
	file, ext, fields, cleanup, err := s.readUpload(w, r)
	if err != nil {
		return // already answered
	}
	defer cleanup()
	tags, err := parseTagsField(fields["tags"])
	if err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	cReq := notestore.CreateRequest{SourceKind: "audio", Audio: bytes.NewReader(file), AudioExt: ext, Tags: tags}
	if v := fields["state"]; v != "" {
		cReq.State = v
	}
	if v := fields["priority"]; v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "priority must be an integer")
			return
		}
		cReq.Priority = p
	}
	if err := parseTimes(optField(fields, "remind_at"), optField(fields, "defer_until"), &cReq.RemindAt, &cReq.DeferUntil); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	n, err := s.store.Create(cReq)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	jsonOut(w, http.StatusCreated, map[string]string{"id": n.ID})
}

// GET /v1/notes (DESIGN.md 5.4). All reads from the index.
func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := index.Filters{Now: s.store.Now()}
	f.States = q["state"]
	for _, st := range f.States {
		if !validState(st) {
			jsonErr(w, http.StatusBadRequest, "invalid state "+st)
			return
		}
	}
	f.Tags = q["tag"]
	f.Q = q.Get("q")
	if v := q.Get("min_age"); v != "" {
		d, err := parseAge(v)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid min_age "+v)
			return
		}
		f.MinAge = d
	}
	if v := q.Get("max_age"); v != "" {
		d, err := parseAge(v)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid max_age "+v)
			return
		}
		f.MaxAge = d
	}
	if v := q.Get("modified_since"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid modified_since "+v)
			return
		}
		f.ModifiedSince = &t
	}
	if v, err := parseBool(q.Get("remind_due")); err != nil || v == nil {
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid remind_due")
			return
		}
	} else {
		f.RemindDue = *v
	}
	if v, err := parseBool(q.Get("defer_due")); err != nil || v == nil {
		if err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid defer_due")
			return
		}
	} else {
		f.DeferDue = *v
	}
	if v := q.Get("processing_status"); v != "" {
		if v != "pending" && v != "running" && v != "failed" {
			jsonErr(w, http.StatusBadRequest, "invalid processing_status "+v)
			return
		}
		f.ProcessingStatus = v
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			jsonErr(w, http.StatusBadRequest, "invalid limit")
			return
		}
		f.Limit = n
	}
	if v := q.Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			jsonErr(w, http.StatusBadRequest, "invalid offset")
			return
		}
		f.Offset = n
	}
	res, err := s.idx.Query(f)
	if err != nil {
		s.log.Error("list query", "err", err)
		jsonErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	jsonOut(w, http.StatusOK, res)
}

// GET /v1/notes/:id (DESIGN.md 5.5). Truth comes from the file.
func (s *Server) handleGet(w http.ResponseWriter, r *http.Request, id string) {
	n, err := s.store.Read(id)
	if err != nil {
		s.storeErr(w, err)
		return
	}
	jsonOut(w, http.StatusOK, n)
}

// POST /v1/notes/:id/append (DESIGN.md 5.3).
func (s *Server) handleAppend(w http.ResponseWriter, r *http.Request, id string) {
	ct := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "multipart/form-data"):
		file, ext, _, cleanup, err := s.readUpload(w, r)
		if err != nil {
			return
		}
		defer cleanup()
		n, err := s.store.AppendAudio(id, bytes.NewReader(file), ext)
		if err != nil {
			s.storeErr(w, err)
			return
		}
		jsonOut(w, http.StatusOK, n)
	case strings.HasPrefix(ct, "application/json"):
		var req struct {
			Text *string `json:"text"`
		}
		if err := decodeJSON(r, &req); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		if req.Text == nil {
			jsonErr(w, http.StatusBadRequest, "text is required")
			return
		}
		n, err := s.store.AppendText(id, *req.Text)
		if err != nil {
			s.storeErr(w, err)
			return
		}
		jsonOut(w, http.StatusOK, n)
	default:
		jsonErr(w, http.StatusUnsupportedMediaType, "application/json or multipart/form-data required")
	}
}

// PATCH /v1/notes/:id (DESIGN.md 5.7).
func (s *Server) handlePatch(w http.ResponseWriter, r *http.Request, id string) {
	var req struct {
		BaseModified *string          `json:"base_modified"`
		State        *string          `json:"state"`
		Tags         *json.RawMessage `json:"tags"`
		Priority     *int             `json:"priority"`
		RemindAt     *json.RawMessage `json:"remind_at"`
		DeferUntil   *json.RawMessage `json:"defer_until"`
		EntryEdit    *struct {
			ID   int    `json:"id"`
			Text string `json:"text"`
		} `json:"entry_edit"`
	}
	if err := decodeJSON(r, &req); err != nil {
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.BaseModified == nil {
		jsonErr(w, http.StatusBadRequest, "base_modified is required")
		return
	}
	base, err := time.Parse(time.RFC3339, *req.BaseModified)
	if err != nil {
		jsonErr(w, http.StatusBadRequest, "base_modified must be an ISO 8601 timestamp")
		return
	}
	p := notestore.Patch{State: req.State, Priority: req.Priority}
	if req.Tags != nil {
		tags, err := parseTags(req.Tags)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
		p.Tags = &tags
	}
	if req.RemindAt != nil {
		p.SetRemindAt = true
		if p.RemindAt, err = parseNullableTime(*req.RemindAt, "remind_at"); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.DeferUntil != nil {
		p.SetDeferUntil = true
		if p.DeferUntil, err = parseNullableTime(*req.DeferUntil, "defer_until"); err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if req.EntryEdit != nil {
		p.EntryEdit = &notestore.EntryEdit{ID: req.EntryEdit.ID, Text: req.EntryEdit.Text}
	}
	n, err := s.store.Edit(id, p, base)
	if errors.Is(err, notestore.ErrStale) {
		// 409 plus the full current note so the client can merge (5.7).
		// The log names the loser: which note and what base the client
		// claimed, so "why did my write bounce" is answerable per request.
		s.log.Debug("patch conflict", "id", id,
			"client_base_modified", *req.BaseModified,
			"current_modified", n.Modified)
		jsonOut(w, http.StatusConflict, n)
		return
	}
	if err != nil {
		s.storeErr(w, err)
		return
	}
	jsonOut(w, http.StatusOK, n)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request, id string) {
	if err := s.store.Delete(id); err != nil {
		s.storeErr(w, err)
		return
	}
	jsonOut(w, http.StatusOK, map[string]string{"id": id})
}

// POST /v1/admin/reindex (DESIGN.md 5.11).
func (s *Server) handleReindex(w http.ResponseWriter, r *http.Request) {
	notes, err := s.store.All()
	if err != nil {
		s.log.Warn("reindex skipped unreadable files", "err", err)
	}
	n, err := s.idx.Reindex(notes)
	if err != nil {
		s.log.Error("reindex", "err", err)
		jsonErr(w, http.StatusInternalServerError, "reindex failed")
		return
	}
	// The admin triggered this by hand: give the receipt at INFO.
	s.log.Info("reindex complete", "scanned", len(notes), "reindexed", n)
	jsonOut(w, http.StatusOK, map[string]int{"reindexed": n})
}

// --- errors and helpers ---------------------------------------------------------

// storeErr maps Note Store sentinels to HTTP codes. Unrecognized errors are
// client-caused validation failures (400) unless they are store internals.
func (s *Server) storeErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, notestore.ErrNotFound):
		jsonErr(w, http.StatusNotFound, "note not found")
	case errors.Is(err, notestore.ErrCorrupt):
		s.log.Error("corrupt note file", "err", err)
		jsonErr(w, http.StatusInternalServerError, "note file corrupt")
	case errors.Is(err, notestore.ErrPendingTranscription):
		jsonErr(w, http.StatusConflict, "transcription in flight; retry after it lands")
	case errors.Is(err, notestore.ErrEntryNotFound):
		jsonErr(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, notestore.ErrUnsupportedAudio):
		jsonErr(w, http.StatusUnsupportedMediaType, err.Error())
	default:
		jsonErr(w, http.StatusBadRequest, err.Error())
	}
}

// readUpload parses a multipart upload: enforces max_upload_mb (413) and
// the supported audio extensions (415). It answers the request itself on
// failure and returns a non-nil error; on success the caller receives the
// raw file bytes, the normalized extension, the plain form fields and a
// cleanup func.
func (s *Server) readUpload(w http.ResponseWriter, r *http.Request) ([]byte, string, map[string]string, func(), error) {
	// Slack for form framing around the file itself.
	r.Body = http.MaxBytesReader(w, r.Body, s.maxUpload+(1<<20))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			jsonErr(w, http.StatusRequestEntityTooLarge, "file too large")
		} else {
			jsonErr(w, http.StatusBadRequest, "malformed multipart form")
		}
		return nil, "", nil, func() {}, err
	}
	cleanup := func() {
		if r.MultipartForm != nil {
			r.MultipartForm.RemoveAll()
		}
	}
	fh, hdr, err := r.FormFile("file")
	if err != nil {
		cleanup()
		jsonErr(w, http.StatusBadRequest, "file field is required")
		return nil, "", nil, func() {}, err
	}
	defer fh.Close()
	data, err := io.ReadAll(fh)
	if err != nil {
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			cleanup()
			jsonErr(w, http.StatusRequestEntityTooLarge, "file too large")
			return nil, "", nil, func() {}, err
		}
		cleanup()
		jsonErr(w, http.StatusBadRequest, "unreadable file")
		return nil, "", nil, func() {}, err
	}
	if int64(len(data)) > s.maxUpload {
		cleanup()
		jsonErr(w, http.StatusRequestEntityTooLarge, "file too large")
		return nil, "", nil, func() {}, fmt.Errorf("too large")
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(hdr.Filename), "."))
	if !notestore.SupportedAudioExt(ext) {
		cleanup()
		jsonErr(w, http.StatusUnsupportedMediaType, "unsupported audio format ."+ext)
		return nil, "", nil, func() {}, fmt.Errorf("bad ext")
	}
	fields := map[string]string{}
	for k, vs := range r.MultipartForm.Value {
		if len(vs) > 0 {
			fields[k] = vs[0]
		}
	}
	return data, ext, fields, cleanup, nil
}

func decodeJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxJSONBody)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON body: %v", err)
	}
	return nil
}

// parseTags accepts both shapes the docs show: ["a","b"] (by human) and
// [{"name":"a","by":"agent"}]. The choice is recorded in the DoD report.
func parseTags(raw *json.RawMessage) ([]notestore.Tag, error) {
	if raw == nil {
		return nil, nil
	}
	var strs []string
	if err := json.Unmarshal(*raw, &strs); err == nil {
		tags := make([]notestore.Tag, 0, len(strs))
		for _, s := range strs {
			if strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("tag with empty name")
			}
			tags = append(tags, notestore.Tag{Name: s, By: "human"})
		}
		return tags, nil
	}
	var tags []notestore.Tag
	if err := json.Unmarshal(*raw, &tags); err != nil {
		return nil, fmt.Errorf("tags must be [\"name\",...] or [{\"name\":...,\"by\":...}]")
	}
	for i := range tags {
		if tags[i].By == "" {
			tags[i].By = "human"
		}
	}
	return tags, nil
}

func parseTagsField(v string) ([]notestore.Tag, error) {
	if v == "" {
		return nil, nil
	}
	raw := json.RawMessage(v)
	return parseTags(&raw)
}

func optField(fields map[string]string, key string) *string {
	if v, ok := fields[key]; ok && v != "" {
		return &v
	}
	return nil
}

func parseTimes(remind, deferUntil *string, outR, outD **time.Time) error {
	if remind != nil {
		t, err := time.Parse(time.RFC3339, *remind)
		if err != nil {
			return fmt.Errorf("remind_at must be ISO 8601")
		}
		v := t.UTC()
		*outR = &v
	}
	if deferUntil != nil {
		t, err := time.Parse(time.RFC3339, *deferUntil)
		if err != nil {
			return fmt.Errorf("defer_until must be ISO 8601")
		}
		v := t.UTC()
		*outD = &v
	}
	return nil
}

// parseNullableTime decodes a PATCH timestamp field: JSON null clears it.
func parseNullableTime(raw json.RawMessage, name string) (*time.Time, error) {
	if string(raw) == "null" {
		return nil, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("%s must be a string or null", name)
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return nil, fmt.Errorf("%s must be ISO 8601", name)
	}
	v := t.UTC()
	return &v, nil
}

var validStates = map[string]bool{
	"inbox": true, "todo": true, "in_progress": true,
	"done": true, "someday": true, "archived": true,
}

func validState(s string) bool { return validStates[s] }

// parseAge accepts Go durations plus day suffixes ("7d", "36h", "45m").
func parseAge(v string) (time.Duration, error) {
	if strings.HasSuffix(v, "d") {
		n, err := strconv.Atoi(strings.TrimSuffix(v, "d"))
		if err != nil || n < 0 {
			return 0, fmt.Errorf("bad duration %q", v)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(v)
}

func parseBool(v string) (*bool, error) {
	switch v {
	case "":
		return nil, nil
	case "true":
		t := true
		return &t, nil
	case "false":
		f := false
		return &f, nil
	}
	return nil, fmt.Errorf("must be true or false")
}

func jsonOut(w http.ResponseWriter, code int, v any) {
	if m := metaOf(w); m != nil && m.status == 0 {
		m.status = code
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func jsonErr(w http.ResponseWriter, code int, msg string) {
	if m := metaOf(w); m != nil {
		m.reason = msg
	}
	jsonOut(w, code, map[string]string{"error": msg})
}
