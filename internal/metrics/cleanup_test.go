package metrics

import (
	"fmt"
	"testing"

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

func makeEntries(n int) map[string]labeledValue {
	m := make(map[string]labeledValue, n)
	for i := range n {
		k := fmt.Sprintf("addr-%d", i)
		m[k] = labeledValue{
			value:  float64(i),
			labels: []attribute.KeyValue{attribute.String("k", k)},
		}
	}
	return m
}

func TestCleanupPrunes(t *testing.T) {
	setupCleanupTest(t)
	gauge := noop.Float64ObservableGauge{}

	metricsMutex.Lock()
	labeledValues[gauge] = makeEntries(150)
	metricsMutex.Unlock()

	cleanupLabeledValues()

	metricsMutex.RLock()
	got := len(labeledValues[gauge])
	metricsMutex.RUnlock()

	if got != 100 {
		t.Errorf("len after cleanup = %d, want 100", got)
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
	labeledValues[big] = makeEntries(200)
	labeledValues[small] = makeEntries(50)
	metricsMutex.Unlock()

	cleanupLabeledValues()

	metricsMutex.RLock()
	bigLen := len(labeledValues[big])
	smallLen := len(labeledValues[small])
	metricsMutex.RUnlock()

	if bigLen != 100 {
		t.Errorf("big metric len = %d, want 100", bigLen)
	}
	if smallLen != 50 {
		t.Errorf("small metric len = %d, want 50", smallLen)
	}
}
