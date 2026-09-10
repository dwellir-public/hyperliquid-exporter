package monitors

import (
	"sync"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

const (
	degradedRateFraction = 0.8   // short rate < 80% of baseline => degraded
	fastGapAlpha         = 0.2   // ~ last 10-20 blocks
	slowGapAlpha         = 0.005 // ~ last few hundred blocks
	emaWarmupBlocks      = 300   // no degraded verdicts before this
	stallFloorSeconds    = 3.0   // min gap before "no blocks" counts as stall
	maxAccountGapSeconds = 900.0 // clamp for accounting deltas
	lagAlpha             = 0.2   // apply-lag EMA, ~ last 10-20 blocks
	degradedLagSeconds   = 30.0  // apply lag above this => degraded
	lagWarmupBlocks      = 5     // no lag verdicts before this
)

// parentQuality attributes block-rate health to whichever peer is parent.
// Degraded = short-window block rate below degradedRateFraction of the
// long-run baseline (rate-band), drifting behind the chain tip (apply lag),
// or an outright stall.
type parentQuality struct {
	mu            sync.Mutex
	now           func() time.Time // injectable for tests, defaults to time.Now
	parentIP      string
	lastAccount   time.Time // wall clock of last tenure/degraded accounting
	lastBlock     time.Time // wall clock of last block event, drives stall detection
	lastBlockTime time.Time // chain timestamp of last block, drives gap EMAs
	fastGapEMA    float64   // seconds, short-window inter-block gap
	slowGapEMA    float64   // seconds, long-run baseline gap
	samples       int       // blocks seen, for warmup
	lagEMA        float64   // seconds, wall clock minus chain timestamp at apply
	lagSamples    int       // blocks with a lag sample, for warmup
}

func newParentQuality() *parentQuality {
	return &parentQuality{now: time.Now}
}

var quality = newParentQuality()

// setter func vars, overridable in tests (pattern: removePeerMetrics, peermon/monitor.go:21)
var (
	addParentPeerTenure       = metrics.AddParentPeerTenure
	addParentPeerDegraded     = metrics.AddParentPeerDegraded
	incrementParentPeerBlocks = metrics.IncrementParentPeerBlocks
	setParentPeerBlockLag     = metrics.SetParentPeerBlockLag
	recordPropagationLatency  = metrics.RecordPropagationLatency
)

// unknownParent labels propagation samples taken before a parent is known
// (or when --peer-latency is off).
const unknownParent = "unknown"

// Parent returns the current parent peer IP, or unknownParent.
func (q *parentQuality) Parent() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.parentIP == "" {
		return unknownParent
	}
	return q.parentIP
}

// SetParent switches attribution to a new parent peer, accounting elapsed
// time to the old parent first.
func (q *parentQuality) SetParent(ip string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.account(false)
	q.parentIP = ip
	q.lastAccount = q.now()
}

// OnBlock is called per fast-state block with the chain block timestamp.
func (q *parentQuality) OnBlock(blockTime time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if !q.lastBlockTime.IsZero() {
		gap := blockTime.Sub(q.lastBlockTime).Seconds()
		// non-monotonic guard: skip EMA update for gap <= 0
		if gap > 0 {
			if q.samples == 0 {
				q.fastGapEMA = gap
				q.slowGapEMA = gap
			} else {
				q.fastGapEMA = fastGapAlpha*gap + (1-fastGapAlpha)*q.fastGapEMA
				q.slowGapEMA = slowGapAlpha*gap + (1-slowGapAlpha)*q.slowGapEMA
			}
			q.samples++
		}
	}

	// apply lag: how far behind the chain tip this node is running. The EMA
	// spans parent switches on purpose — lag is node state; a parent that
	// catches the node up pulls it down, one that lets it drift pushes it up.
	if lag := q.now().Sub(blockTime).Seconds(); lag >= 0 {
		if q.lagSamples == 0 {
			q.lagEMA = lag
		} else {
			q.lagEMA = lagAlpha*lag + (1-lagAlpha)*q.lagEMA
		}
		q.lagSamples++
	}

	q.account(false)

	if q.parentIP != "" {
		incrementParentPeerBlocks(q.parentIP)
		setParentPeerBlockLag(q.parentIP, q.lagEMA)
	}

	q.lastBlockTime = blockTime
	q.lastBlock = q.now()
}

// Flush performs periodic accounting so stalls are covered even when no
// blocks arrive.
func (q *parentQuality) Flush() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.account(true)
}

// account attributes elapsed wall time since lastAccount to the current
// parent as tenure, and as degraded time when the degraded verdict holds.
// includeStall enables the stall trigger (Flush only). Caller must hold mu.
func (q *parentQuality) account(includeStall bool) {
	now := q.now()
	if q.parentIP == "" || q.lastAccount.IsZero() {
		q.lastAccount = now
		return
	}

	delta := now.Sub(q.lastAccount).Seconds()
	if delta > maxAccountGapSeconds {
		delta = maxAccountGapSeconds
	}

	addParentPeerTenure(q.parentIP, delta)
	if q.degraded() || (includeStall && q.stalled(now)) {
		addParentPeerDegraded(q.parentIP, delta)
	}

	q.lastAccount = now
}

// degraded reports whether the parent is letting the node run unhealthily:
// short-window block rate persistently below baseline (rate-band), or apply
// lag past the drift threshold. The rate-band catches rate collapse; the lag
// check catches steady-but-behind drift, which produces normal inter-block
// gaps and is invisible to the rate-band. Caller must hold mu.
func (q *parentQuality) degraded() bool {
	if q.samples >= emaWarmupBlocks && q.fastGapEMA > q.slowGapEMA/degradedRateFraction {
		return true
	}
	return q.lagging()
}

// lagging reports the apply-lag verdict: the node is applying blocks well
// behind their chain timestamps. Caller must hold mu.
func (q *parentQuality) lagging() bool {
	return q.lagSamples >= lagWarmupBlocks && q.lagEMA > degradedLagSeconds
}

// stalled reports whether blocks have stopped arriving outright.
// Caller must hold mu.
func (q *parentQuality) stalled(now time.Time) bool {
	if q.lastBlock.IsZero() {
		return false
	}
	threshold := stallFloorSeconds
	if s := 4 * q.slowGapEMA; s > threshold {
		threshold = s
	}
	return now.Sub(q.lastBlock).Seconds() > threshold
}
