package monitors

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
	"github.com/validaoxyz/hyperliquid-exporter/internal/utils"
)

const tcpTrafficStream = "tcp_traffic"

// peerSample is one endpoint's traffic in a tcp_traffic record, summed across
// ports. port is the port of the largest single flow, for probe preference.
type peerSample struct {
	ip    string
	port  int
	value float64
}

// tcpTrafficRecord is one parsed tcp_traffic line: ["ts",[[["In"|"Out","IP",port],value],...]].
// inbound and outbound are sorted by value descending, then IP ascending.
type tcpTrafficRecord struct {
	timestamp time.Time
	inbound   []peerSample
	outbound  []peerSample
}

type tcpTrafficParseError struct {
	stage string
	err   error
}

func (e *tcpTrafficParseError) Error() string { return e.stage + ": " + e.err.Error() }

func rowError(msg string) error {
	return &tcpTrafficParseError{stage: "row", err: errors.New(msg)}
}

// parseTCPTrafficRecord decodes one line strictly: any malformed row rejects
// the whole record so a partial snapshot never becomes a peer observation.
func parseTCPTrafficRecord(line []byte) (tcpTrafficRecord, error) {
	var outer []json.RawMessage
	if err := json.Unmarshal(line, &outer); err != nil || len(outer) != 2 {
		return tcpTrafficRecord{}, &tcpTrafficParseError{stage: "record", err: errors.New("outer record must have arity two")}
	}
	var tsString string
	if err := unmarshalRequiredJSON(outer[0], &tsString); err != nil {
		return tcpTrafficRecord{}, &tcpTrafficParseError{stage: "timestamp", err: err}
	}
	timestamp, ok := parseVisorTime(tsString)
	if !ok {
		return tcpTrafficRecord{}, &tcpTrafficParseError{stage: "timestamp", err: errors.New("invalid timestamp")}
	}
	if !rawJSONArray(outer[1]) {
		return tcpTrafficRecord{}, &tcpTrafficParseError{stage: "record", err: errors.New("flow payload must be an array")}
	}
	var flows []json.RawMessage
	if err := json.Unmarshal(outer[1], &flows); err != nil {
		return tcpTrafficRecord{}, &tcpTrafficParseError{stage: "record", err: err}
	}

	acc := map[string]map[string]*peerSample{"in": {}, "out": {}}
	maxFlow := map[string]map[string]float64{"in": {}, "out": {}} // largest single flow per IP, for port choice
	for _, raw := range flows {
		var pair []json.RawMessage
		if err := json.Unmarshal(raw, &pair); err != nil || len(pair) != 2 {
			return tcpTrafficRecord{}, rowError("flow must have arity two")
		}
		var key []json.RawMessage
		if err := json.Unmarshal(pair[0], &key); err != nil || len(key) != 3 {
			return tcpTrafficRecord{}, rowError("flow key must have arity three")
		}
		var dirString, ipString string
		var port uint16
		var value float64
		if err := unmarshalRequiredJSON(key[0], &dirString); err != nil {
			return tcpTrafficRecord{}, rowError("invalid direction")
		}
		if err := unmarshalRequiredJSON(key[1], &ipString); err != nil {
			return tcpTrafficRecord{}, rowError("invalid IP")
		}
		if err := unmarshalRequiredJSON(key[2], &port); err != nil || port == 0 {
			return tcpTrafficRecord{}, rowError("invalid port")
		}
		if err := unmarshalRequiredJSON(pair[1], &value); err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return tcpTrafficRecord{}, rowError("value must be finite and nonnegative")
		}
		ip, err := netip.ParseAddr(ipString)
		if err != nil || ip.Zone() != "" {
			return tcpTrafficRecord{}, rowError("invalid IP")
		}
		ipString = ip.Unmap().String()

		var dir string
		switch dirString {
		case "In":
			dir = "in"
		case "Out":
			dir = "out"
		default:
			return tcpTrafficRecord{}, rowError("unknown direction")
		}
		s, exists := acc[dir][ipString]
		if !exists {
			s = &peerSample{ip: ipString}
			acc[dir][ipString] = s
		}
		if s.port == 0 || value > maxFlow[dir][ipString] {
			s.port = int(port)
			maxFlow[dir][ipString] = value
		}
		s.value += value
		if math.IsInf(s.value, 0) {
			return tcpTrafficRecord{}, rowError("endpoint aggregate overflow")
		}
	}

	return tcpTrafficRecord{
		timestamp: timestamp,
		inbound:   sortedSamples(acc["in"]),
		outbound:  sortedSamples(acc["out"]),
	}, nil
}

func sortedSamples(values map[string]*peerSample) []peerSample {
	out := make([]peerSample, 0, len(values))
	for _, s := range values {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].value != out[j].value {
			return out[i].value > out[j].value
		}
		return ipLess(out[i].ip, out[j].ip)
	})
	return out
}

// ipLess orders addresses numerically (10.0.0.1 before 2.0.0.1 is false),
// falling back to string order for anything that does not parse.
func ipLess(a, b string) bool {
	pa, errA := netip.ParseAddr(a)
	pb, errB := netip.ParseAddr(b)
	if errA != nil || errB != nil {
		return a < b
	}
	return pa.Compare(pb) < 0
}

// tcpTrafficConsumer receives every parsed record once. onPoll runs after
// each poll whether or not records arrived, for time-based state.
type tcpTrafficConsumer interface {
	onRecord(rec tcpTrafficRecord, receivedAt time.Time, seeding bool)
	onPoll(now time.Time)
}

// TCPTrafficMonitor tails tcp_traffic once and fans each record out to its
// consumers, so the file is parsed a single time per poll.
type TCPTrafficMonitor struct {
	dir       string
	tail      tailState
	consumers []tcpTrafficConsumer
	now       func() time.Time
}

func NewTCPTrafficMonitor(cfg *config.Config, consumers ...tcpTrafficConsumer) *TCPTrafficMonitor {
	return &TCPTrafficMonitor{
		dir:       filepath.Join(cfg.NodeHome, "data", "tcp_traffic", "hourly"),
		consumers: consumers,
		now:       time.Now,
	}
}

func StartTCPTrafficMonitor(ctx context.Context, cfg *config.Config, consumers ...tcpTrafficConsumer) {
	m := NewTCPTrafficMonitor(cfg, consumers...)

	if _, err := os.Stat(m.dir); os.IsNotExist(err) {
		logger.InfoComponent("gossip", "tcp_traffic directory not found, peer discovery and parent selection disabled")
		metrics.SetSourceUp(tcpTrafficStream, false)
		return
	}

	logger.InfoComponent("gossip", "Starting tcp_traffic monitor")
	m.monitor(ctx)
}

func (m *TCPTrafficMonitor) monitor(ctx context.Context) {
	ticker := time.NewTicker(gossipPollInterval)
	defer ticker.Stop()

	// seed from the current hour so peers and the parent are known at once;
	// counters are suppressed because Prometheus already recorded them
	if filePath, err := utils.LatestFile(m.dir); err == nil && filePath != "" {
		m.tail.path = filePath
		logger.InfoComponent("gossip", "tcp_traffic monitor processing %s", filePath)
		if newOffset, err := m.processFile(filePath, 0, true); err != nil {
			logger.DebugComponent("gossip", "Error processing tcp_traffic: %v", err)
		} else {
			m.tail.offset = newOffset
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.poll()
		}
	}
}

func (m *TCPTrafficMonitor) poll() {
	filePath, err := utils.LatestFile(m.dir)
	if err != nil || filePath == "" {
		metrics.SetSourceUp(tcpTrafficStream, false)
		m.finishPoll()
		return
	}

	if filePath != m.tail.path {
		logger.InfoComponent("gossip", "tcp_traffic monitor switching to %s", filePath)
	}

	err = m.tail.poll(filePath, func(path string, offset int64) (int64, error) {
		return m.processFile(path, offset, false)
	})
	if err != nil {
		logger.DebugComponent("gossip", "Error reading tcp_traffic: %v", err)
	}
	metrics.SetSourceUp(tcpTrafficStream, err == nil)
	m.finishPoll()
}

func (m *TCPTrafficMonitor) finishPoll() {
	now := m.now()
	for _, c := range m.consumers {
		c.onPoll(now)
	}
}

// processFile parses every committed line from offset and hands each record
// to all consumers. Malformed records are counted and skipped.
func (m *TCPTrafficMonitor) processFile(filePath string, offset int64, seeding bool) (int64, error) {
	newOffset, err := readCommittedLines(filePath, offset, func(line []byte) {
		rec, err := parseTCPTrafficRecord(line)
		if err != nil {
			// replayed history was already counted before the restart
			if !seeding {
				stage := "record"
				var perr *tcpTrafficParseError
				if errors.As(err, &perr) {
					stage = perr.stage
				}
				metrics.IncrementParseErrors(tcpTrafficStream, stage)
			}
			return
		}
		now := m.now()
		metrics.MarkSourceSample(tcpTrafficStream, now)
		for _, c := range m.consumers {
			c.onRecord(rec, now, seeding)
		}
	})
	if err != nil {
		return offset, err
	}
	return newOffset, nil
}
