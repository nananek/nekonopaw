package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/nananek/nekonopaw/internal/api"
	"github.com/nananek/nekonopaw/internal/pw"
)

//go:embed web
var webFS embed.FS

func main() {
	listen := flag.String("listen", "127.0.0.1:8080", "bind address (host:port). Tailscale IP を指定する想定")
	flag.Parse()

	pw.Init()
	defer pw.Deinit()

	client, err := pw.Connect()
	if err != nil {
		log.Fatalf("pipewire connect: %v", err)
	}
	defer client.Close()
	log.Printf("connected to pipewire daemon")

	staticFS, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatalf("embed: %v", err)
	}

	srv := &http.Server{
		Addr:              *listen,
		Handler:           api.New(client, staticFS).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("listening on %s", *listen)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http: %v", err)
		}
	}()

	<-ctx.Done()
	log.Printf("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
}
