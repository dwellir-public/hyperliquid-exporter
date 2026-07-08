package metrics

import (
	"fmt"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	api "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

func setupCleanupTest(t *testing.T) {
	t.Helper()
	metricsMutex.Lock()
	labeledValues = make(map[api.Observable]map[string]labeledValue)
	metricsMutex.Unlock()
	t.Cleanup(func() {
		metricsMutex.Lock()
		labeledValues = make(map[api.Observable]map[string]labeledValue)
		metricsMutex.Unlock()
	})
}

// makeEntries creates n entries where addr-0 is the oldest and addr-(n-1) the newest.
func makeEntries(n int) map[string]labeledValue {
	base := time.Now().Add(-time.Duration(n) * time.Second)
	m := make(map[string]labeledValue, n)
	for i := range n {
		k := fmt.Sprintf("addr-%d", i)
		m[k] = labeledValue{
			value:     float64(i),
			labels:    []attribute.KeyValue{attribute.String("k", k)},
			updatedAt: base.Add(time.Duration(i) * time.Second),
		}
	}
	return m
}

func TestCleanupPrunes(t *testing.T) {
	setupCleanupTest(t)
	gauge := noop.Float64ObservableGauge{}

	metricsMutex.Lock()
	labeledValues[gauge] = makeEntries(250)
	metricsMutex.Unlock()

	cleanupLabeledValues()

	metricsMutex.RLock()
	defer metricsMutex.RUnlock()

	if got := len(labeledValues[gauge]); got != 200 {
		t.Fatalf("len after cleanup = %d, want 200", got)
	}
	// the 200 most recently updated entries (addr-50..addr-249) must survive
	for i := 50; i < 250; i++ {
		k := fmt.Sprintf("addr-%d", i)
		if _, ok := labeledValues[gauge][k]; !ok {
			t.Errorf("recent entry %s evicted", k)
		}
	}
}

func TestCleanupUnderLimit(t *testing.T) {
	setupCleanupTest(t)
	gauge := noop.Float64ObservableGauge{}

	metricsMutex.Lock()
	labeledValues[gauge] = makeEntries(50)
	metricsMutex.Unlock()

	cleanupLabeledValues()

	metricsMutex.RLock()
	got := len(labeledValues[gauge])
	metricsMutex.RUnlock()

	if got != 50 {
		t.Errorf("len after cleanup = %d, want 50 (untouched)", got)
	}
}

func TestCleanupMultipleMetrics(t *testing.T) {
	setupCleanupTest(t)
	big := noop.Float64ObservableGauge{}
	small := noop.Int64ObservableGauge{}

	metricsMutex.Lock()
	labeledValues[big] = makeEntries(300)
	labeledValues[small] = makeEntries(50)
	metricsMutex.Unlock()

	cleanupLabeledValues()

	metricsMutex.RLock()
	bigLen := len(labeledValues[big])
	smallLen := len(labeledValues[small])
	metricsMutex.RUnlock()

	if bigLen != 200 {
		t.Errorf("big metric len = %d, want 200", bigLen)
	}
	if smallLen != 50 {
		t.Errorf("small metric len = %d, want 50", smallLen)
	}
}
