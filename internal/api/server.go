// Package api は HTTP server のルーティングと JSON ハンドラ。
package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"strconv"

	"github.com/nananek/nekonopaw/internal/pw"
)

// Server は HTTP routing をまとめる。
type Server struct {
	mux     *http.ServeMux
	backend pw.Backend
}

// New は backend と静的ファイル fs を受け取り Server を組み立てる。
func New(backend pw.Backend, staticFS fs.FS) *Server {
	mux := http.NewServeMux()
	s := &Server{mux: mux, backend: backend}

	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/streams", s.streams)
	mux.HandleFunc("GET /api/sinks", s.sinks)
	mux.HandleFunc("PUT /api/streams/{id}/volume", s.setStreamVolume)
	mux.HandleFunc("PUT /api/streams/{id}/mute", s.setStreamMute)
	mux.HandleFunc("PUT /api/sinks/{id}/volume", s.setSinkVolume)
	mux.HandleFunc("PUT /api/sinks/{id}/mute", s.setSinkMute)
	mux.HandleFunc("PUT /api/default-sink", s.setDefaultSink)

	mux.Handle("/", http.FileServerFS(staticFS))

	return s
}

// Handler returns the underlying mux as http.Handler.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) streams(w http.ResponseWriter, r *http.Request) {
	streams, err := s.backend.Streams(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, streams)
}

func (s *Server) sinks(w http.ResponseWriter, r *http.Request) {
	sinks, err := s.backend.Sinks(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, sinks)
}

type volumeBody struct {
	VolumePct int `json:"volume_pct"`
}

type muteBody struct {
	Mute bool `json:"mute"`
}

type defaultSinkBody struct {
	Name string `json:"name"`
}

func (s *Server) setStreamVolume(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var body volumeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.backend.SetStreamVolume(r.Context(), id, body.VolumePct); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setStreamMute(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var body muteBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.backend.SetStreamMute(r.Context(), id, body.Mute); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setSinkVolume(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var body volumeBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.backend.SetSinkVolume(r.Context(), id, body.VolumePct); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setSinkMute(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(w, r)
	if !ok {
		return
	}
	var body muteBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.backend.SetSinkMute(r.Context(), id, body.Mute); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) setDefaultSink(w http.ResponseWriter, r *http.Request) {
	var body defaultSinkBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.backend.SetDefaultSink(r.Context(), body.Name); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseID(w http.ResponseWriter, r *http.Request) (uint32, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return 0, false
	}
	return uint32(id), true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}
