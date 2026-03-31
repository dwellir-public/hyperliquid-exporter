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

func TestRemovePeerReachable(t *testing.T) {
	setupPeerMetricsTest(t)

	SetPeerReachable("1.2.3.4", "outbound", true)
	RemovePeerReachable("1.2.3.4", "outbound")

	metricsMutex.RLock()
	defer metricsMutex.RUnlock()
	if _, exists := labeledValues[HLPeerReachableGauge]["1.2.3.4:outbound"]; exists {
		t.Fatalf("expected peer reachable entry to be removed")
	}
}

func TestRemovePeerMetrics(t *testing.T) {
	setupPeerMetricsTest(t)

	SetPeerLatency("1.2.3.4", "outbound", 123)
	SetPeerReachable("1.2.3.4", "outbound", false)
	SetPeerLatency("1.2.3.4", "inbound", 456)
	RemovePeerMetrics("1.2.3.4")

	metricsMutex.RLock()
	defer metricsMutex.RUnlock()

	if _, exists := labeledValues[HLPeerLatencyGauge]["1.2.3.4:outbound"]; exists {
		t.Fatalf("expected peer latency outbound entry to be removed")
	}
	if _, exists := labeledValues[HLPeerLatencyGauge]["1.2.3.4:inbound"]; exists {
		t.Fatalf("expected peer latency inbound entry to be removed")
	}
	if _, exists := labeledValues[HLPeerReachableGauge]["1.2.3.4:outbound"]; exists {
		t.Fatalf("expected peer reachable entry to be removed")
	}
}
