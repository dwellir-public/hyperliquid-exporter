package monitors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	api "go.opentelemetry.io/otel/metric"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/logger"
	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
	"github.com/validaoxyz/hyperliquid-exporter/internal/utils"
)

const (
	visorStream       = "visor"
	visorPollInterval = 10 * time.Second
)

// visorState mirrors the JSON record hl-visor writes. Extra fields are
// tolerated; optional fields decode independently so one bad type cannot
// suppress the valid height and timing fields.
type visorState struct {
	InitialHeight         int64
	Height                int64
	ScheduledFreezeHeight *int64
	HardforkVersion       *int64
	ConsensusTime         string
	WallClockTime         string
	ReferenceLagSeconds   *float64
}

// latestVisorHeight is shared with the node_state monitor so it can derive
// height-above-freeze without re-reading the visor JSON. Readers want the
// most recent value; dropped updates are fine.
var latestVisorHeight atomic.Int64

// StartVisorMonitor watches the visor sync state from two hl-visor sources:
// hyperliquid_data/visor_abci_state.json (live snapshot, preferred) and the
// rolling data/visor_abci_states/hourly log as a fallback.
func StartVisorMonitor(ctx context.Context, cfg *config.Config) {
	snapshotPath := filepath.Join(cfg.NodeHome, "hyperliquid_data", "visor_abci_state.json")
	historicalDir := filepath.Join(cfg.NodeHome, "data", "visor_abci_states", "hourly")
	logger.InfoComponent("visor", "Watching %s (fallback %s)", snapshotPath, historicalDir)

	ticker := time.NewTicker(visorPollInterval)
	defer ticker.Stop()

	tickVisor(snapshotPath, historicalDir)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tickVisor(snapshotPath, historicalDir)
		}
	}
}

func tickVisor(snapshotPath, historicalDir string) bool {
	state, ts, err := readLatestVisorState(snapshotPath, historicalDir)
	if err != nil {
		metrics.SetSourceUp(visorStream, false)
		if !os.IsNotExist(err) {
			metrics.IncrementSourceErrors(visorStream, "read")
		}
		// node_state must not derive height-above-freeze from a stale height
		latestVisorHeight.Store(0)
		logger.DebugComponent("visor", "no visor state yet: %v", err)
		return false
	}
	publishVisorState(state)
	metrics.SetSourceUp(visorStream, true)
	metrics.MarkSourceSample(visorStream, ts)
	return true
}

// readLatestVisorState prefers the live snapshot. The sample time is the
// record's wall_clock_time, written when the visor took the sample; file
// mtime is the fallback. The hourly log is read only when the snapshot is
// missing or invalid; its last complete line is ["<iso>", {<state>}].
func readLatestVisorState(snapshotPath, historicalDir string) (visorState, time.Time, error) {
	if data, err := os.ReadFile(snapshotPath); err == nil {
		if s, jerr := decodeVisorState(data); jerr == nil && s.Height > 0 {
			if t, ok := parseVisorTime(s.WallClockTime); ok {
				return s, t, nil
			}
			if info, err := os.Stat(snapshotPath); err == nil {
				return s, info.ModTime(), nil
			}
			return s, time.Now(), nil
		}
	}

	latest, err := utils.LatestFile(historicalDir)
	if err != nil {
		return visorState{}, time.Time{}, err
	}
	if latest == "" {
		return visorState{}, time.Time{}, os.ErrNotExist
	}
	data, err := os.ReadFile(latest)
	if err != nil {
		return visorState{}, time.Time{}, err
	}
	line, ok := lastFullLine(data)
	if !ok {
		return visorState{}, time.Time{}, fmt.Errorf("no complete record in %s", latest)
	}
	var record []json.RawMessage
	if err := json.Unmarshal(line, &record); err != nil || len(record) != 2 {
		return visorState{}, time.Time{}, fmt.Errorf("decode record: expected [ts, state]")
	}
	var tsStr string
	if err := unmarshalRequiredJSON(record[0], &tsStr); err != nil {
		return visorState{}, time.Time{}, fmt.Errorf("decode ts: %w", err)
	}
	s, err := decodeVisorState(record[1])
	if err != nil {
		return visorState{}, time.Time{}, fmt.Errorf("decode state: %w", err)
	}
	if s.Height <= 0 {
		return visorState{}, time.Time{}, fmt.Errorf("decode state: invalid height %d", s.Height)
	}
	ts, ok := parseVisorTime(tsStr)
	if !ok {
		return visorState{}, time.Time{}, fmt.Errorf("invalid historical timestamp %q", tsStr)
	}
	return s, ts, nil
}

// lastFullLine returns the newest non-empty newline-terminated record; an
// unterminated suffix is never treated as a record.
func lastFullLine(data []byte) ([]byte, bool) {
	end := bytes.LastIndexByte(data, '\n') // committed region is data[:end]
	for end >= 0 {
		start := bytes.LastIndexByte(data[:end], '\n') + 1
		if line := bytes.TrimSpace(data[start:end]); len(line) > 0 {
			return line, true
		}
		end = start - 1
	}
	return nil, false
}

// decodeVisorState decodes independently optional scalar fields
// independently: a transitional node writing hardfork_version with an
// unexpected type must not suppress height and timing.
func decodeVisorState(data []byte) (visorState, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return visorState{}, fmt.Errorf("decode visor object: %w", err)
	}
	var s visorState
	decode := func(key string, dst any) error {
		raw, ok := fields[key]
		if !ok {
			return nil
		}
		return json.Unmarshal(raw, dst)
	}
	if err := decode("initial_height", &s.InitialHeight); err != nil {
		return visorState{}, fmt.Errorf("decode initial_height: %w", err)
	}
	if err := decode("height", &s.Height); err != nil {
		return visorState{}, fmt.Errorf("decode height: %w", err)
	}
	_ = decode("consensus_time", &s.ConsensusTime)
	_ = decode("wall_clock_time", &s.WallClockTime)
	s.ScheduledFreezeHeight = decodeOptionalInt(fields["scheduled_freeze_height"])
	s.HardforkVersion = decodeOptionalInt(fields["hardfork_version"])
	s.ReferenceLagSeconds = decodeOptionalFloat(fields["reference_lag_seconds"])
	if s.ReferenceLagSeconds == nil {
		s.ReferenceLagSeconds = decodeOptionalFloat(fields["reference_lag"])
	}
	return s, nil
}

func decodeOptionalInt(raw json.RawMessage) *int64 {
	var value int64
	if unmarshalRequiredJSON(raw, &value) != nil || value < 0 {
		return nil
	}
	return &value
}

func decodeOptionalFloat(raw json.RawMessage) *float64 {
	var value float64
	if unmarshalRequiredJSON(raw, &value) != nil {
		return nil
	}
	return &value
}

func publishVisorState(s visorState) {
	metrics.SetGauge(metrics.HLVisorHeight, float64(s.Height))
	metrics.SetGauge(metrics.HLVisorInitialHeight, float64(s.InitialHeight))
	latestVisorHeight.Store(s.Height)
	applied := 0.0
	if s.Height > 0 && s.InitialHeight > 0 && s.Height >= s.InitialHeight {
		applied = float64(s.Height - s.InitialHeight)
	}
	metrics.SetGauge(metrics.HLVisorBlocksApplied, applied)

	setOptionalGauge(metrics.HLVisorScheduledFreezeHeight, intPtrFloat(s.ScheduledFreezeHeight))
	hfSource := attribute.String("source", "visor_state")
	if s.HardforkVersion == nil {
		metrics.ClearGaugeSeries(metrics.HLVisorHardforkVersion, hfSource)
	} else {
		metrics.SetGaugeSeries(metrics.HLVisorHardforkVersion, float64(*s.HardforkVersion), hfSource)
	}
	setOptionalGauge(metrics.HLVisorReferenceLagSeconds, s.ReferenceLagSeconds)

	consensusT, okC := parseVisorTime(s.ConsensusTime)
	wallT, okW := parseVisorTime(s.WallClockTime)
	if okC && okW {
		metrics.SetGauge(metrics.HLVisorConsensusAheadOfWallSecond, consensusT.Sub(wallT).Seconds())
	} else {
		metrics.ClearGauge(metrics.HLVisorConsensusAheadOfWallSecond)
	}
}

func intPtrFloat(p *int64) *float64 {
	if p == nil {
		return nil
	}
	f := float64(*p)
	return &f
}

// setOptionalGauge publishes v or withdraws the gauge when v is nil, so a
// missing field is absent rather than zero.
func setOptionalGauge(g api.Float64ObservableGauge, v *float64) {
	if v == nil {
		metrics.ClearGauge(g)
		return
	}
	metrics.SetGauge(g, *v)
}
