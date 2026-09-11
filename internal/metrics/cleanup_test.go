package metrics

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	api "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

func resetLabeledState(t *testing.T) {
	t.Helper()
	reset := func() {
		metricsMutex.Lock()
		labeledValues = make(map[api.Observable]map[string]labeledValue)
		lastVotes = make(map[string]labeledValue)
		metricsMutex.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

func TestPruneStaleVotes(t *testing.T) {
	resetLabeledState(t)
	now := time.Now()
	metricsMutex.Lock()
	lastVotes["stale"] = labeledValue{updatedAt: now.Add(-2 * voteMaxAge)}
	lastVotes["fresh"] = labeledValue{updatedAt: now}
	metricsMutex.Unlock()

	pruneStaleVotes(voteMaxAge)

	metricsMutex.RLock()
	defer metricsMutex.RUnlock()
	if _, ok := lastVotes["stale"]; ok {
		t.Error("stale vote not pruned")
	}
	if _, ok := lastVotes["fresh"]; !ok {
		t.Error("fresh vote pruned")
	}
}

func TestRemoveValidatorSeries(t *testing.T) {
	resetLabeledState(t)
	// distinct noop instruments so the two families are separate map keys
	stake := &noop.Float64ObservableGauge{}
	latency := &noop.Float64ObservableGauge{}
	heartbeat := &noop.Float64ObservableGauge{}
	savedStake, savedLatency, savedHB := HLConsensusValidatorStakeGauge, HLConsensusValidatorLatencyGauge, HLConsensusHeartbeatStatusGauge
	HLConsensusValidatorStakeGauge, HLConsensusValidatorLatencyGauge, HLConsensusHeartbeatStatusGauge = stake, latency, heartbeat
	t.Cleanup(func() {
		HLConsensusValidatorStakeGauge, HLConsensusValidatorLatencyGauge, HLConsensusHeartbeatStatusGauge = savedStake, savedLatency, savedHB
	})

	entry := labeledValue{value: 1, labels: []attribute.KeyValue{attribute.String("validator", "0xAB")}}
	metricsMutex.Lock()
	labeledValues[stake] = map[string]labeledValue{"0xAB": entry, "0xcd": entry}
	labeledValues[latency] = map[string]labeledValue{"0xab": entry, "0xcd": entry}
	lastVotes["0xab"] = entry
	labeledValues[heartbeat] = map[string]labeledValue{"0xab_since_last_success": entry, "0xcd_since_last_success": entry}
	metricsMutex.Unlock()

	RemoveValidatorLatencySeries("0xAB")
	metricsMutex.RLock()
	if _, ok := labeledValues[latency]["0xab"]; ok {
		t.Error("latency series not removed by lowercase key")
	}
	if _, ok := labeledValues[stake]["0xAB"]; !ok {
		t.Error("stake series removed by latency-only call")
	}
	metricsMutex.RUnlock()

	RemoveValidatorSeries("0xAB")
	metricsMutex.RLock()
	defer metricsMutex.RUnlock()
	if _, ok := labeledValues[stake]["0xAB"]; ok {
		t.Error("stake series not removed")
	}
	if _, ok := lastVotes["0xab"]; ok {
		t.Error("vote entry not removed")
	}
	if _, ok := labeledValues[stake]["0xcd"]; !ok {
		t.Error("unrelated validator removed")
	}
	if _, ok := labeledValues[heartbeat]["0xab_since_last_success"]; ok {
		t.Error("heartbeat status series not removed")
	}
	if _, ok := labeledValues[heartbeat]["0xcd_since_last_success"]; !ok {
		t.Error("unrelated heartbeat status removed")
	}
}

func TestGetValidatorName(t *testing.T) {
	RegisterValidatorInfo("0xabc", "0xsigner", "Moniker")
	if got := GetValidatorName("0xABC"); got != "Moniker" {
		t.Errorf("GetValidatorName = %q, want Moniker", got)
	}
	if got := GetValidatorName("0xmissing"); got != "" {
		t.Errorf("GetValidatorName for unknown = %q, want empty", got)
	}
}
