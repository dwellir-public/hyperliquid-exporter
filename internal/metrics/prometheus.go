package metrics

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
)

func StartPrometheusServer(ctx context.Context, port int) error {
	mux := http.NewServeMux()

	// TimeoutHandler buffers the response and safely replies 503 on timeout,
	// avoiding concurrent writes to the ResponseWriter
	promHandler := http.TimeoutHandler(promhttp.Handler(), 30*time.Second, "Metrics collection timed out\n")
	mux.Handle("/metrics", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Debug("Metrics endpoint called from %s", r.RemoteAddr)
		promHandler.ServeHTTP(w, r)
	}))

	// health check endpoint for debugging
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK\n"))
	})

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	go func() {
		logger.Info("Starting Prometheus metrics server on port %d", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
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
