package monitors

import (
	"context"
	"sync"
	"testing"

	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

var metricsOnce sync.Once

func initTestMetrics(t *testing.T) {
	t.Helper()
	metricsOnce.Do(func() {
		cfg := metrics.MetricsConfig{
			Chain: "test",
			Alias: "test-node",
		}
		if err := metrics.InitMetrics(context.Background(), cfg); err != nil {
			t.Fatalf("failed to init metrics: %v", err)
		}
	})
}
