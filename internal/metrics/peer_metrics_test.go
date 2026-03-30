package metrics

import (
	"context"
	"sync"
	"testing"

	api "go.opentelemetry.io/otel/metric"
)

var peerMetricsOnce sync.Once

func setupPeerMetricsTest(t *testing.T) {
	t.Helper()

	peerMetricsOnce.Do(func() {
		cfg := MetricsConfig{
			Chain: "test",
			Alias: "test-node",
		}
		if err := InitMetrics(context.Background(), cfg); err != nil {
			t.Fatalf("failed to init metrics: %v", err)
		}
	})

	metricsMutex.Lock()
	labeledValues = make(map[api.Observable]map[string]labeledValue)
	currentValues = make(map[api.Observable]any)
	metricsMutex.Unlock()
}

func TestRemoveIncomingPeerLastSeen(t *testing.T) {
	setupPeerMetricsTest(t)

	SetIncomingPeerLastSeen("1.2.3.4", 123)
	RemoveIncomingPeerLastSeen("1.2.3.4")

	metricsMutex.RLock()
	defer metricsMutex.RUnlock()
	if _, exists := labeledValues[HLP2PIncomingPeerLastSeenGauge]["1.2.3.4"]; exists {
		t.Fatalf("expected incoming peer last seen entry to be removed")
	}
}

func TestRemoveChildPeerMetrics(t *testing.T) {
	setupPeerMetricsTest(t)

	SetChildPeerConnected("1.2.3.4", true, true)
	SetChildPeerConnections("1.2.3.4", 2)

	RemoveChildPeerConnected("1.2.3.4", true)
	RemoveChildPeerConnections("1.2.3.4")

	metricsMutex.RLock()
	defer metricsMutex.RUnlock()

	if _, exists := labeledValues[HLP2PChildPeerConnectedGauge]["1.2.3.4:true"]; exists {
		t.Fatalf("expected child peer connected entry to be removed")
	}
	if _, exists := labeledValues[HLP2PChildPeerConnectionsGauge]["1.2.3.4"]; exists {
		t.Fatalf("expected child peer connections entry to be removed")
	}
}
