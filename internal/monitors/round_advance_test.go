package monitors

import (
	"encoding/json"
	"testing"
)

func TestParseRoundAdvanceReason(t *testing.T) {
	tests := []struct {
		name        string
		raw         string
		wantReason  string
		wantSuspect string
		wantErr     bool
	}{
		{"qc string", `"Qc"`, "qc", "", false},
		{"tc string", `"Tc"`, "tc", "", false},
		{"legacy timeout string", `"timeout"`, "tc", "", false},
		{"unknown string", `"Foo"`, "other", "", false},
		{"tc tagged object", `{"Tc":{"last_vote_round":11,"next_proposer":{"Ok":"0x1111..1111"},"proposer":{"Ok":"0x2222..2222"},"suspect":"NoVote"}}`, "tc", "NoVote", false},
		{"tc tagged without suspect", `{"Tc":{"last_vote_round":11}}`, "tc", "", false},
		{"null", `null`, "", "", true},
		{"empty", ``, "", "", true},
		{"two variants", `{"Tc":{},"Qc":{}}`, "", "", true},
		{"malformed", `{`, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reason, suspect, err := parseRoundAdvanceReason(json.RawMessage(tt.raw))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if reason != tt.wantReason || suspect != tt.wantSuspect {
				t.Errorf("got (%q, %q), want (%q, %q)", reason, suspect, tt.wantReason, tt.wantSuspect)
			}
		})
	}
}

func TestProcessConsensusLineRoundAdvance(t *testing.T) {
	m := newTestConsensusMonitor(t)
	lines := []string{
		`["2026-08-09T01:05:06.914547237",["round advance",{"prev_round":819363188,"round":819363189,"reason":{"Tc":{"last_vote_round":819363188,"next_proposer":{"Ok":"0x1111..1111"},"proposer":{"Ok":"0x2222..2222"},"suspect":"NoVote"}}}]]`,
		`["2026-08-09T01:05:06.914547237",["round advance",{"prev_round":1,"round":2,"reason":"Qc"}]]`,
	}
	for _, line := range lines {
		if err := m.processConsensusLine(line); err != nil {
			t.Errorf("processConsensusLine(%s): %v", line, err)
		}
	}
	if err := m.processConsensusLine(`["2026-08-09T01:05:06.914547237",["round advance",{"prev_round":1,"round":2,"reason":null}]]`); err == nil {
		t.Error("expected error for null reason")
	}
}
