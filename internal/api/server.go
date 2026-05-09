// Package api は HTTP server のルーティングと JSON ハンドラ。
package api

import (
	"encoding/json"
	"io/fs"
	"net/http"

	"github.com/nananek/nekonopaw/internal/pw"
)

// Server は HTTP routing をまとめる。
type Server struct {
	mux    *http.ServeMux
	client *pw.Client
}

// New は pw.Client と静的ファイル fs を受け取り Server を組み立てる。
func New(client *pw.Client, staticFS fs.FS) *Server {
	mux := http.NewServeMux()
	s := &Server{mux: mux, client: client}

	mux.HandleFunc("GET /api/health", s.health)
	mux.HandleFunc("GET /api/streams", s.streams) // stub
	mux.HandleFunc("GET /api/sinks", s.sinks)     // stub
	mux.Handle("/", http.FileServerFS(staticFS))

	return s
}

// Handler returns the underlying mux as http.Handler.
func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// streams は sink-input (アプリ毎の出力) 一覧。 TODO: 実装
func (s *Server) streams(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

// sinks は出力デバイス一覧。 TODO: 実装
func (s *Server) sinks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []any{})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
