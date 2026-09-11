package monitors

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/validaoxyz/hyperliquid-exporter/internal/actiontypes"
	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
	"github.com/validaoxyz/hyperliquid-exporter/internal/replica"
)

// schemaStreams maps each consumed hl-node log stream to a strict parse of one
// line. The drift test feeds every line of testdata/schema/<stream>.jsonl
// (committed, trimmed) and testdata/live/<stream>*.jsonl (gitignored, pulled
// from a node by the schema-watch routine) through it and fails on the first
// rejection, so a changed log shape shows up before it silently zeroes a
// metric in production.
var schemaStreams = map[string]func(t *testing.T, line []byte) error{
	"node_fast_block_times": func(t *testing.T, line []byte) error {
		return parseBlockTimeLine(context.Background(), string(line), "fast")
	},
	"node_slow_block_times": func(t *testing.T, line []byte) error {
		return parseBlockTimeLine(context.Background(), string(line), "slow")
	},
	"block_times": func(t *testing.T, line []byte) error {
		return parseLegacyBlockTimeLine(context.Background(), string(line))
	},
	"consensus": func(t *testing.T, line []byte) error {
		return newSchemaConsensusMonitor(t).processConsensusLine(string(line))
	},
	"status": func(t *testing.T, line []byte) error {
		return newSchemaConsensusMonitor(t).processStatusLine(string(line))
	},
	"validator_status": func(t *testing.T, line []byte) error {
		return processValidatorStatusLine(string(line))
	},
	"evm_block_and_receipts": func(t *testing.T, line []byte) error {
		return processEVMBlockAndReceiptsLine(string(line))
	},
	"replica_cmds": func(t *testing.T, line []byte) error {
		p := replica.NewParser(1)
		block, err := p.ParseBlockFromLine(line)
		if err != nil {
			return err
		}
		defer p.ReturnBlock(block)
		m, err := p.ExtractMetrics(block)
		if err != nil {
			return err
		}
		if n := m.ActionCounts[actiontypes.Other]; n > 0 {
			return fmt.Errorf("%d action(s) of unknown type; extend internal/actiontypes", n)
		}
		return nil
	},
	"gossip_rpc": func(t *testing.T, line []byte) error {
		ev, stage := parseGossipRPCLine(line)
		if stage != "" {
			return fmt.Errorf("stage %s", stage)
		}
		if ev.eventType == "child_peers status" {
			var peers [][]json.RawMessage
			if err := json.Unmarshal(ev.data[1], &peers); err != nil {
				return fmt.Errorf("stage payload: %w", err)
			}
		}
		return nil
	},
	"gossip_connections": func(t *testing.T, line []byte) error {
		ev, stage := parseGossipConnectionLine(line)
		if stage != "" {
			return fmt.Errorf("stage %s", stage)
		}
		if !ev.known {
			return fmt.Errorf("unknown event tag %q; extend the gossip_connections allowlist", ev.tag)
		}
		return nil
	},
	"tcp_traffic": func(t *testing.T, line []byte) error {
		_, err := parseTCPTrafficRecord(line)
		return err
	},
}

func newSchemaConsensusMonitor(t *testing.T) *ConsensusMonitor {
	t.Helper()
	return NewConsensusMonitor(&config.Config{NodeHome: t.TempDir()})
}

func TestSchemaFixtures(t *testing.T) {
	initBlockGlobals(t)
	for stream, parse := range schemaStreams {
		t.Run(stream, func(t *testing.T) {
			committed := filepath.Join("testdata", "schema", stream+".jsonl")
			live, _ := filepath.Glob(filepath.Join("testdata", "live", stream+"*.jsonl"))
			files := append([]string{committed}, live...)
			for _, path := range files {
				checkSchemaFile(t, path, parse)
			}
		})
	}
}

func checkSchemaFile(t *testing.T, path string, parse func(*testing.T, []byte) error) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("missing fixture: %v", err)
	}
	defer func() { _ = f.Close() }()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	n := 0
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		n++
		if err := parse(t, append([]byte(nil), line...)); err != nil {
			t.Errorf("%s:%d rejected: %v", path, lineNo, err)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if n == 0 {
		t.Errorf("%s has no records", path)
	}
}
