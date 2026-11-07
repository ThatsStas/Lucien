// stub_whisper is the phase-1 stand-in for the Whisper Turbo service
// (DESIGN.md 7.1, IMPLEMENTATION-FRAMEWORK.md 5).
//
// Fidelity notes — where the stub differs from the real service, per the
// framework rule that every simplification must be documented here:
//   - No transcription happens. Success mode returns a fixed
//     {"text": <transcript>} from --transcript.
//   - GET /v1/audio/health returns {"status":"ok","model":"stub"}; the real
//     service also returns device info. Same status code.
//   - 422 bodies are simplified: the real FastAPI service answers
//     {"detail": [...validation errors...]}, the stub answers
//     {"detail": "reason"}. Status codes match.
//   - "refuse" mode closes the connection without any response, emulating a
//     refused/dropped endpoint; the real failure would be a connection
//     refusal by the OS, indistinguishable at our HTTP client layer.
//   - response_format and temperature are accepted and recorded but always
//     behave as response_format=json.
//
// Usage (system tests start this, never an operator):
//
//	stub_whisper --port 0 [--mode ok|error500|error422|refuse|delay]
//	             [--transcript TEXT] [--delay DUR] [--record FILE.ndjson]
//
// It prints "addr=127.0.0.1:<port>" on stderr once listening, records every
// transcription request in memory (GET /__requests) and optionally appends
// NDJSON lines to --record.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// Request is one recorded transcription attempt.
type Request struct {
	TS       string            `json:"ts"`
	Filename string            `json:"filename"`
	Size     int               `json:"size"`
	SHA256   string            `json:"sha256"`
	Fields   map[string]string `json:"fields"`
}

// Server implements the whisper surface the adapter uses.
type Server struct {
	Mode       string
	Transcript string
	Delay      time.Duration
	RecordFile string

	mu  sync.Mutex
	all []*Request // every request ever received, mirrored to RecordFile
}

var validModes = map[string]bool{
	"ok": true, "error500": true, "error422": true, "refuse": true, "delay": true,
}

// mode returns the current behavior mode (mutex-protected; the mode can be
// switched at runtime by tests via POST /__mode).
func (s *Server) mode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Mode
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && (r.URL.Path == "/v1/audio/health" || r.URL.Path == "/v1/audio/transcriptions/health"):
		writeJSON(w, 200, map[string]string{"status": "ok", "model": "stub"})
	case r.Method == http.MethodGet && r.URL.Path == "/__requests":
		s.mu.Lock()
		all := s.all
		s.mu.Unlock()
		writeJSON(w, 200, all)
	case r.Method == http.MethodPost && r.URL.Path == "/__mode":
		var in struct {
			Mode string `json:"mode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || !validModes[in.Mode] {
			writeJSON(w, 400, map[string]string{"detail": "mode must be ok|error500|error422|refuse|delay"})
			return
		}
		s.mu.Lock()
		s.Mode = in.Mode
		s.mu.Unlock()
		writeJSON(w, 200, map[string]string{"mode": in.Mode})
	case r.Method == http.MethodPost && r.URL.Path == "/v1/audio/transcriptions":
		s.transcribe(w, r)
	default:
		writeJSON(w, 404, map[string]string{"detail": "Not Found"})
	}
}

func (s *Server) transcribe(w http.ResponseWriter, r *http.Request) {
	mode := s.mode()
	if mode == "refuse" {
		if hj, ok := w.(http.Hijacker); ok {
			conn, _, err := hj.Hijack()
			if err == nil {
				conn.Close()
				return
			}
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}

	rec := Request{TS: time.Now().UTC().Format(time.RFC3339Nano), Fields: map[string]string{}}
	err := r.ParseMultipartForm(1 << 20)
	if err != nil {
		s.record(rec)
		writeJSON(w, 422, map[string]string{"detail": "malformed multipart form"})
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		s.record(rec)
		writeJSON(w, 422, map[string]string{"detail": "file field required"})
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		s.record(rec)
		writeJSON(w, 422, map[string]string{"detail": "unreadable file"})
		return
	}
	sum := sha256.Sum256(data)
	rec.Filename = hdr.Filename
	rec.Size = len(data)
	rec.SHA256 = hex.EncodeToString(sum[:])
	for k, v := range r.PostForm {
		if k != "file" && len(v) > 0 {
			rec.Fields[k] = v[0]
		}
	}
	s.record(rec)

	switch mode {
	case "error500":
		writeJSON(w, 500, map[string]string{"detail": "stub internal error"})
	case "error422":
		writeJSON(w, 422, map[string]string{"detail": "stub unprocessable"})
	case "delay":
		time.Sleep(s.Delay)
		writeJSON(w, 200, map[string]string{"text": s.Transcript})
	default:
		writeJSON(w, 200, map[string]string{"text": s.Transcript})
	}
}

func (s *Server) record(rec Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.all = append(s.all, &rec)
	if s.RecordFile != "" {
		if f, err := os.OpenFile(s.RecordFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			line, _ := json.Marshal(rec)
			f.Write(append(line, '\n'))
			f.Close()
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func main() {
	port := flag.Int("port", 0, "port to bind on 127.0.0.1 (0 = ephemeral)")
	mode := flag.String("mode", "ok", "ok|error500|error422|refuse|delay")
	transcript := flag.String("transcript", "stub transcript", "transcript returned on success")
	delay := flag.Duration("delay", 0, "response delay for mode=delay")
	record := flag.String("record", "", "append NDJSON request records to this file")
	flag.Parse()

	srv := &Server{Mode: *mode, Transcript: *transcript, Delay: *delay, RecordFile: *record}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		log.Fatalf("stub_whisper: listen: %v", err)
	}
	fmt.Fprintf(os.Stderr, "event=stub_listen addr=%s mode=%s\n", ln.Addr(), *mode)
	log.Fatal(http.Serve(ln, srv))
}
