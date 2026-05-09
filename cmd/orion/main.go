// Command orion is the long-running Orion service.
//
// In E0-1 it serves only a /health endpoint. The orchestration loops
// (Conductor, Lookout, Refiner) land in subsequent epics.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/revelara-ai/orion/internal/version"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	flag.Parse()

	log.Printf("starting %s on %s", version.String(), *addr)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler)

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Run server in a goroutine so we can handle signals.
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	// Wait for SIGINT/SIGTERM or server error.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		log.Fatalf("server error: %v", err)
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("graceful shutdown failed: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
