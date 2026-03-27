package monitors

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/validaoxyz/hyperliquid-exporter/internal/config"
)

func newTestConsensusMonitor(t *testing.T) *ConsensusMonitor {
	t.Helper()
	initTestMetrics(t)
	return NewConsensusMonitor(&config.Config{})
}

func TestProcessConsensusLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantErr bool
	}{
		{
			name:    "vote message",
			line:    `["2025-01-01T00:00:00.000000000", ["in", {"source":"peer1","msg":{"Vote":{"round":5,"signer_id":"0xabc123"}}}]]`,
			wantErr: false,
		},
		{
			name:    "block message with QC",
			line:    `["2025-01-01T00:00:00.000000000", ["in", {"source":"peer1","msg":{"Block":{"round":10,"proposer":"0xdef456","qc":{"signers":["0xa","0xb","0xc"]}}}}]]`,
			wantErr: false,
		},
		{
			name:    "heartbeat out",
			line:    `["2025-01-01T00:00:00.000000000", ["out", {"Heartbeat":{"validator":"0xabc","random_id":42}}]]`,
			wantErr: false,
		},
		{
			name:    "heartbeat ack in",
			line:    `["2025-01-01T00:00:00.000000000", ["in", {"source":"0xpeer","msg":{"HeartbeatAck":{"random_id":42}}}]]`,
			wantErr: false,
		},
		{
			name:    "unknown message type ignored",
			line:    `["2025-01-01T00:00:00.000000000", ["in", {"source":"p","msg":{"Unknown":{}}}]]`,
			wantErr: false,
		},
		{
			name:    "malformed JSON",
			line:    `not-json`,
			wantErr: true,
		},
		{
			name:    "empty line",
			line:    ``,
			wantErr: true,
		},
		{
			name:    "non-array line",
			line:    `{"key":"value"}`,
			wantErr: true,
		},
		{
			name:    "too few outer elements",
			line:    `["2025-01-01T00:00:00.000000000"]`,
			wantErr: true,
		},
		{
			name:    "invalid timestamp",
			line:    `["not-a-timestamp", ["in", {}]]`,
			wantErr: true,
		},
		{
			name:    "inner array too short",
			line:    `["2025-01-01T00:00:00.000000000", ["in"]]`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestConsensusMonitor(t)
			err := m.processConsensusLine(tt.line)
			if (err != nil) != tt.wantErr {
				t.Errorf("processConsensusLine() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestProcessBlockRaw(t *testing.T) {
	t.Run("block with QC signers", func(t *testing.T) {
		m := newTestConsensusMonitor(t)

		block := ConsensusBlockMessage{
			Round:    42,
			Proposer: "0xproposer",
			QC: &QCEvidence{
				Signers: []string{"0xsig1", "0xsig2", "0xsig3"},
			},
		}
		data, _ := json.Marshal(block)

		if err := m.processBlockRaw(data); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// verify QC signatures tracked
		if len(m.qcSignatures) != 3 {
			t.Errorf("expected 3 QC signature entries, got %d", len(m.qcSignatures))
		}

		// verify round tracked
		if m.lastBlockRound != 42 {
			t.Errorf("expected lastBlockRound=42, got %d", m.lastBlockRound)
		}
	})

	t.Run("block with TC", func(t *testing.T) {
		m := newTestConsensusMonitor(t)

		tc := TCData{
			Timeouts: []struct {
				Validator string `json:"validator"`
			}{
				{Validator: "0xval1"},
				{Validator: "0xval2"},
			},
		}
		tcData, _ := json.Marshal(tc)

		block := ConsensusBlockMessage{
			Round:    50,
			Proposer: "0xproposer",
			TC:       tcData,
		}
		data, _ := json.Marshal(block)

		if err := m.processBlockRaw(data); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if len(m.tcVotes) != 2 {
			t.Errorf("expected 2 TC vote entries, got %d", len(m.tcVotes))
		}
	})

	t.Run("block without QC or TC", func(t *testing.T) {
		m := newTestConsensusMonitor(t)

		block := ConsensusBlockMessage{
			Round:    1,
			Proposer: "0xprop",
		}
		data, _ := json.Marshal(block)

		if err := m.processBlockRaw(data); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		m := newTestConsensusMonitor(t)
		if err := m.processBlockRaw(json.RawMessage(`{broken`)); err == nil {
			t.Error("expected error for malformed JSON")
		}
	})

	t.Run("rounds per block tracking", func(t *testing.T) {
		m := newTestConsensusMonitor(t)

		// first block
		b1 := ConsensusBlockMessage{Round: 10, Proposer: "0xp"}
		d1, _ := json.Marshal(b1)
		if err := m.processBlockRaw(d1); err != nil {
			t.Fatal(err)
		}

		// second block
		b2 := ConsensusBlockMessage{Round: 13, Proposer: "0xp"}
		d2, _ := json.Marshal(b2)
		if err := m.processBlockRaw(d2); err != nil {
			t.Fatal(err)
		}

		if m.lastBlockRound != 13 {
			t.Errorf("expected lastBlockRound=13, got %d", m.lastBlockRound)
		}
	})
}

func TestProcessHeartbeatOut(t *testing.T) {
	t.Run("valid heartbeat", func(t *testing.T) {
		m := newTestConsensusMonitor(t)

		hb := &HeartbeatMessage{Validator: "0xval123", RandomID: 99}
		ts := time.Now()

		if err := m.processHeartbeatOut(hb, ts); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		m.heartbeatsMutex.RLock()
		_, exists := m.heartbeats[99]
		m.heartbeatsMutex.RUnlock()

		if !exists {
			t.Error("heartbeat not stored")
		}
	})

	t.Run("missing validator", func(t *testing.T) {
		m := newTestConsensusMonitor(t)
		hb := &HeartbeatMessage{Validator: "", RandomID: 1}
		if err := m.processHeartbeatOut(hb, time.Now()); err == nil {
			t.Error("expected error for missing validator")
		}
	})

	t.Run("missing random_id", func(t *testing.T) {
		m := newTestConsensusMonitor(t)
		hb := &HeartbeatMessage{Validator: "0xval", RandomID: 0}
		if err := m.processHeartbeatOut(hb, time.Now()); err == nil {
			t.Error("expected error for zero random_id")
		}
	})
}

func TestProcessHeartbeatAck(t *testing.T) {
	t.Run("matching ack", func(t *testing.T) {
		m := newTestConsensusMonitor(t)

		// register a heartbeat first
		hb := &HeartbeatMessage{Validator: "0xval", RandomID: 77}
		ts := time.Now().Add(-100 * time.Millisecond)
		_ = m.processHeartbeatOut(hb, ts)

		// process ack
		ack := &HeartbeatAckMessage{RandomID: 77}
		if err := m.processHeartbeatAck(ack, "0xpeer", time.Now()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("unmatched ack", func(t *testing.T) {
		m := newTestConsensusMonitor(t)
		ack := &HeartbeatAckMessage{RandomID: 999}
		// should return nil (not found, not an error)
		if err := m.processHeartbeatAck(ack, "0xpeer", time.Now()); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing random_id", func(t *testing.T) {
		m := newTestConsensusMonitor(t)
		ack := &HeartbeatAckMessage{RandomID: 0}
		if err := m.processHeartbeatAck(ack, "0xpeer", time.Now()); err == nil {
			t.Error("expected error for zero random_id")
		}
	})

	t.Run("missing source", func(t *testing.T) {
		m := newTestConsensusMonitor(t)
		ack := &HeartbeatAckMessage{RandomID: 1}
		if err := m.processHeartbeatAck(ack, "", time.Now()); err == nil {
			t.Error("expected error for empty source")
		}
	})
}

func TestProcessStatusLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantErr bool
	}{
		{
			name:    "valid status with both fields",
			line:    `["2025-01-01T00:00:00Z", {"disconnected_validators":[],"heartbeat_statuses":[]}]`,
			wantErr: false,
		},
		{
			name:    "valid status with disconnected validators",
			line:    `["2025-01-01T00:00:00Z", {"disconnected_validators":[["0xval1",[["0xpeer1",5]]]],"heartbeat_statuses":[]}]`,
			wantErr: false,
		},
		{
			name:    "valid status with heartbeat statuses",
			line:    `["2025-01-01T00:00:00Z", {"disconnected_validators":[],"heartbeat_statuses":[["0xval1",{"since_last_success":1.5,"last_ack_duration":0.3}]]}]`,
			wantErr: false,
		},
		{
			name:    "malformed JSON",
			line:    `broken`,
			wantErr: true,
		},
		{
			name:    "too few elements",
			line:    `["only-one"]`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := newTestConsensusMonitor(t)
			err := m.processStatusLine(tt.line)
			if (err != nil) != tt.wantErr {
				t.Errorf("processStatusLine() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestProcessDisconnectedValidatorsRaw(t *testing.T) {
	t.Run("populates disconnected set", func(t *testing.T) {
		m := newTestConsensusMonitor(t)

		data := json.RawMessage(`[["0xval1",[["0xpeer1",5],["0xpeer2",3]]],["0xval2",[["0xpeer3",1]]]]`)
		m.processDisconnectedValidatorsRaw(data)

		m.disconnectedMutex.RLock()
		defer m.disconnectedMutex.RUnlock()

		expected := 3 // val1_peer1, val1_peer2, val2_peer3
		if len(m.disconnectedSet) != expected {
			t.Errorf("expected %d disconnected pairs, got %d", expected, len(m.disconnectedSet))
		}
	})

	t.Run("reconnected validators removed", func(t *testing.T) {
		m := newTestConsensusMonitor(t)

		// initial: val1 disconnected from peer1
		data1 := json.RawMessage(`[["0xval1",[["0xpeer1",5]]]]`)
		m.processDisconnectedValidatorsRaw(data1)

		// update: val1 now connected (empty list)
		data2 := json.RawMessage(`[]`)
		m.processDisconnectedValidatorsRaw(data2)

		m.disconnectedMutex.RLock()
		defer m.disconnectedMutex.RUnlock()
		if len(m.disconnectedSet) != 0 {
			t.Errorf("expected 0 disconnected pairs after reconnect, got %d", len(m.disconnectedSet))
		}
	})

	t.Run("invalid JSON does not panic", func(t *testing.T) {
		m := newTestConsensusMonitor(t)
		m.processDisconnectedValidatorsRaw(json.RawMessage(`not-json`))
	})
}

func TestProcessHeartbeatStatusesRaw(t *testing.T) {
	t.Run("valid statuses", func(t *testing.T) {
		m := newTestConsensusMonitor(t)
		data := json.RawMessage(`[["0xval1",{"since_last_success":2.5,"last_ack_duration":0.1}],["0xval2",{"since_last_success":0.5,"last_ack_duration":0.05}]]`)
		// should not panic
		m.processHeartbeatStatusesRaw(data)
	})

	t.Run("invalid JSON does not panic", func(t *testing.T) {
		m := newTestConsensusMonitor(t)
		m.processHeartbeatStatusesRaw(json.RawMessage(`broken`))
	})

	t.Run("empty array", func(t *testing.T) {
		m := newTestConsensusMonitor(t)
		m.processHeartbeatStatusesRaw(json.RawMessage(`[]`))
	})
}

func TestFormatValidatorAddress(t *testing.T) {
	m := newTestConsensusMonitor(t)

	tests := []struct {
		input string
		want  string
	}{
		{"0xabcdef1234567890", "0xabcd..7890"},
		{"abcdef1234567890", "0xabcd..7890"},
		{"short", "short"},
		{"0x12345678", "0x12345678"}, // 10 chars, not truncated
	}

	for _, tt := range tests {
		got := m.formatValidatorAddress(tt.input)
		if got != tt.want {
			t.Errorf("formatValidatorAddress(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
