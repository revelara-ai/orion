// Command orion is the long-running Orion service.
//
// In E0-1 it serves only a /health endpoint. E2-1 wires the
// Postgres pool + migration runner on startup. The orchestration
// loops (Conductor, Lookout, Refiner) land in subsequent epics.
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

	"github.com/revelara-ai/orion/internal/database"
	"github.com/revelara-ai/orion/internal/scheduler"
	"github.com/revelara-ai/orion/internal/version"
)

func main() {
	addr := flag.String("addr", ":8080", "address to listen on")
	skipDB := flag.Bool("skip-db", false, "skip Postgres init (useful for /health-only test deploys)")
	enableScheduler := flag.Bool("enable-scheduler", false, "enable the detection scheduler loop (Epic 3; off by default until risksink+quiescence wire up)")
	flag.Parse()

	log.Printf("starting %s on %s", version.String(), *addr)

	// Init Postgres + run migrations. Skippable via flag for the
	// minimum-viable deploy that only serves /health.
	if !*skipDB {
		dsn := os.Getenv("POSTGRES_DSN")
		if dsn == "" {
			log.Fatal("POSTGRES_DSN env required (or pass -skip-db)")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		pool, err := database.NewPool(ctx, dsn)
		cancel()
		if err != nil {
			log.Fatalf("database: connect: %v", err)
		}
		defer pool.Close()
		ctx, cancel = context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if err := database.Migrate(ctx, pool); err != nil {
			log.Fatalf("database: migrate: %v", err)
		}
		log.Printf("database: connected + migrations applied")
		// Future epics: instantiate RLSPool and inject into handlers.
		_ = database.NewRLSPool(pool)

		// Optional: detection scheduler. Off by default; turning it on
		// without the rest of the Epic 3 chain (quiescence E3-4,
		// risksink E3-5, loopguard E3-7) would dump findings into
		// Polaris without provenance. The flag exists so the scheduler
		// is observable in dev before the chain is complete.
		if *enableScheduler {
			runCtx, runCancel := context.WithCancel(context.Background())
			defer runCancel()
			sched := scheduler.NewScheduler(scheduler.Scheduler{
				Bindings: func(ctx context.Context) ([]scheduler.BindingDescriptor, error) {
					// Wiring to TrackerBindingRepo.ListAll lands once the
					// full Tick chain is buildable. v1 stub: no bindings.
					return nil, nil
				},
				Tick: func(ctx context.Context, b scheduler.BindingDescriptor) error {
					log.Printf("scheduler: tick stub binding=%s", b.BindingID)
					return nil
				},
				Logger: log.Default(),
			})
			go func() {
				if err := sched.Run(runCtx); err != nil {
					log.Printf("scheduler: %v", err)
				}
			}()
			log.Printf("scheduler: started (stub mode; bindings source not yet wired)")
		}
	}

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
