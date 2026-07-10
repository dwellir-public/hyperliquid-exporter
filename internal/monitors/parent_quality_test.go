package monitors

import (
	"testing"
	"time"
)

// quolClock is a manually advanced clock for injecting into parentQuality.now.
type quolClock struct{ t time.Time }

func (c *quolClock) now() time.Time { return c.t }

func (c *quolClock) advance(d time.Duration) time.Time {
	c.t = c.t.Add(d)
	return c.t
}

// stubQualitySetters overrides the parentQuality package-level setter vars
// with test doubles, restored on cleanup (pattern: removePeerMetrics stub,
// internal/peermon/monitor.go:21).
func stubQualitySetters(t *testing.T) (tenure, degraded map[string]float64, blocks map[string]int) {
	t.Helper()
	tenure = make(map[string]float64)
	degraded = make(map[string]float64)
	blocks = make(map[string]int)

	origTenure, origDegraded, origBlocks, origLag := addParentPeerTenure, addParentPeerDegraded, incrementParentPeerBlocks, setParentPeerBlockLag
	addParentPeerTenure = func(ip string, s float64) { tenure[ip] += s }
	addParentPeerDegraded = func(ip string, s float64) { degraded[ip] += s }
	incrementParentPeerBlocks = func(ip string) { blocks[ip]++ }
	setParentPeerBlockLag = func(string, float64) {}

	t.Cleanup(func() {
		addParentPeerTenure = origTenure
		addParentPeerDegraded = origDegraded
		incrementParentPeerBlocks = origBlocks
		setParentPeerBlockLag = origLag
	})
	return
}

// stubLagGauge overrides the block-lag gauge setter var, restored on cleanup.
func stubLagGauge(t *testing.T) map[string]float64 {
	t.Helper()
	lag := make(map[string]float64)

	orig := setParentPeerBlockLag
	setParentPeerBlockLag = func(ip string, s float64) { lag[ip] = s }
	t.Cleanup(func() { setParentPeerBlockLag = orig })
	return lag
}

func TestParentQuality_ApplyLag(t *testing.T) {
	cases := []struct {
		name         string
		lag          time.Duration
		wantDegraded bool
	}{
		{"drifting_behind", 60 * time.Second, true},
		{"near_tip", 5 * time.Second, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, degraded, _ := stubQualitySetters(t)
			lagGauge := stubLagGauge(t)

			clock := &quolClock{t: time.Unix(1000, 0)}
			q := newParentQuality()
			q.now = clock.now
			q.SetParent("A")

			// steady 1s inter-block gaps, so the rate-band never trips; block
			// timestamps trail the wall clock by tc.lag
			for range 50 {
				clock.advance(time.Second)
				q.OnBlock(clock.t.Add(-tc.lag))
			}

			gotDegraded := degraded["A"] > 0
			if gotDegraded != tc.wantDegraded {
				t.Fatalf("degraded=%v with %v apply lag, want %v (degraded=%v)",
					gotDegraded, tc.lag, tc.wantDegraded, degraded["A"])
			}

			wantLag := tc.lag.Seconds()
			if got := lagGauge["A"]; got < wantLag*0.9 || got > wantLag*1.1 {
				t.Fatalf("lag gauge=%v, want ~%v", got, wantLag)
			}
		})
	}
}

func TestParentQuality_ApplyLagWarmup(t *testing.T) {
	_, degraded, _ := stubQualitySetters(t)
	stubLagGauge(t)

	clock := &quolClock{t: time.Unix(1000, 0)}
	q := newParentQuality()
	q.now = clock.now
	q.SetParent("A")

	// fewer than lagWarmupBlocks samples, all far behind the tip
	for range lagWarmupBlocks - 1 {
		clock.advance(time.Second)
		q.OnBlock(clock.t.Add(-time.Hour))
	}

	if degraded["A"] != 0 {
		t.Fatalf("degraded time accrued before lag warmup complete: %v", degraded["A"])
	}
}

func TestParentQuality_RateBand(t *testing.T) {
	cases := []struct {
		name         string
		steadyGap    time.Duration
		slowGap      time.Duration
		wantDegraded bool
	}{
		{"steady_no_slowdown", time.Second, time.Second, false},
		{"slowdown_2x", time.Second, 2 * time.Second, true},
		{"mild_slowdown_within_band", time.Second, 1200 * time.Millisecond, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenure, degraded, _ := stubQualitySetters(t)

			clock := &quolClock{t: time.Unix(0, 0)}
			q := newParentQuality()
			q.now = clock.now
			q.SetParent("A")

			// warm up past emaWarmupBlocks with steady gaps
			for range emaWarmupBlocks + 1 {
				clock.advance(tc.steadyGap)
				q.OnBlock(clock.t)
			}

			if degraded["A"] != 0 {
				t.Fatalf("degraded time accrued before slowdown: %v", degraded["A"])
			}

			// apply (or continue) the gap pattern under test
			for range 100 {
				clock.advance(tc.slowGap)
				q.OnBlock(clock.t)
			}

			gotDegraded := degraded["A"] > 0
			if gotDegraded != tc.wantDegraded {
				t.Fatalf("degraded=%v after gap change, want %v (tenure=%v, degraded=%v)",
					gotDegraded, tc.wantDegraded, tenure["A"], degraded["A"])
			}
		})
	}
}

func TestParentQuality_Stall(t *testing.T) {
	tenure, degraded, blocks := stubQualitySetters(t)

	clock := &quolClock{t: time.Unix(0, 0)}
	q := newParentQuality()
	q.now = clock.now
	q.SetParent("A")

	clock.advance(time.Second)
	q.OnBlock(clock.t)

	if blocks["A"] != 1 {
		t.Fatalf("expected 1 block attributed to A, got %d", blocks["A"])
	}

	// well past stallFloorSeconds with no further blocks
	clock.advance(5 * time.Second)
	q.Flush()

	if degraded["A"] <= 0 {
		t.Fatalf("expected stall to accrue degraded time, got %v (tenure=%v)", degraded["A"], tenure["A"])
	}
}

func TestParentQuality_SwitchAttribution(t *testing.T) {
	tenure, _, _ := stubQualitySetters(t)

	clock := &quolClock{t: time.Unix(0, 0)}
	q := newParentQuality()
	q.now = clock.now
	q.SetParent("A")

	clock.advance(10 * time.Second)
	q.OnBlock(clock.t)

	aBefore := tenure["A"]
	if aBefore <= 0 {
		t.Fatalf("expected tenure accrued to A, got %v", aBefore)
	}

	clock.advance(5 * time.Second)
	q.SetParent("B")

	if tenure["A"] != aBefore+5 {
		t.Fatalf("pre-switch delta not landed on A: got %v want %v", tenure["A"], aBefore+5)
	}

	clock.advance(10 * time.Second)
	q.OnBlock(clock.t)

	if tenure["A"] != aBefore+5 {
		t.Fatalf("A tenure grew after switch: %v", tenure["A"])
	}
	if tenure["B"] <= 0 {
		t.Fatalf("expected tenure accrued to B after switch, got %v", tenure["B"])
	}
}

func TestParentQuality_WarmupSuppression(t *testing.T) {
	t.Run("rate_band_suppressed", func(t *testing.T) {
		tenure, degraded, _ := stubQualitySetters(t)

		clock := &quolClock{t: time.Unix(0, 0)}
		q := newParentQuality()
		q.now = clock.now
		q.SetParent("A")

		// fewer than emaWarmupBlocks samples, alternating slow/fast gaps
		for i := range emaWarmupBlocks - 1 {
			gap := time.Second
			if i%2 == 0 {
				gap = 3 * time.Second
			}
			clock.advance(gap)
			q.OnBlock(clock.t)
		}

		if degraded["A"] != 0 {
			t.Fatalf("degraded time accrued before warmup complete: %v (tenure=%v)", degraded["A"], tenure["A"])
		}
	})

	t.Run("stall_still_fires", func(t *testing.T) {
		_, degraded, _ := stubQualitySetters(t)

		clock := &quolClock{t: time.Unix(0, 0)}
		q := newParentQuality()
		q.now = clock.now
		q.SetParent("A")

		clock.advance(time.Second)
		q.OnBlock(clock.t) // samples still far under emaWarmupBlocks

		clock.advance(10 * time.Second)
		q.Flush()

		if degraded["A"] <= 0 {
			t.Fatalf("expected stall to fire despite no rate-band warmup")
		}
	})
}
