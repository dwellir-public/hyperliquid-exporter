package metrics

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
)

const (
	scrapeTimeout      = 30 * time.Second
	maxScrapesInFlight = 5
)

func StartPrometheusServer(ctx context.Context, port int) error {
	mux := http.NewServeMux()

	// TimeoutHandler buffers the response and safely replies 503 on timeout,
	// avoiding concurrent writes to the ResponseWriter. The in-flight bound
	// turns a scrape storm into 503s instead of a pile of concurrent gathers.
	promHandler := http.TimeoutHandler(
		promhttp.HandlerFor(prometheus.DefaultGatherer, promhttp.HandlerOpts{MaxRequestsInFlight: maxScrapesInFlight}),
		scrapeTimeout,
		"Metrics collection timed out\n",
	)
	mux.Handle("/metrics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("Metrics endpoint called from %s", r.RemoteAddr)
		promHandler.ServeHTTP(w, r)
	}))

	// health check endpoint for debugging
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK\n"))
	})

	// WriteTimeout exceeds scrapeTimeout so the handler's 503 is written
	// before the connection deadline fires; the rest bound slow clients.
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      scrapeTimeout + 5*time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// bind synchronously so a taken port fails startup instead of leaving a
	// metric-less process running
	ln, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", server.Addr, err)
	}

	go func() {
		logger.Info("Starting Prometheus metrics server on port %d", port)
		if err := server.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Error("Prometheus server error: %v", err)
		}
	}()

	go func() {
		<-ctx.Done()
		if err := server.Shutdown(context.Background()); err != nil {
			logger.Error("Error shutting down Prometheus server: %v", err)
		}
	}()

	return nil
}
