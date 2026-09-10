package monitors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

// processRoundAdvance handles a consensus log entry of the form
// ["round advance", {"prev_round":N,"round":N+1,"reason":...}] and counts
// timeout-certificate (Tc) advances per suspect.
func processRoundAdvance(raw json.RawMessage) error {
	var event struct {
		Reason json.RawMessage `json:"reason"`
	}
	if err := json.Unmarshal(raw, &event); err != nil {
		return fmt.Errorf("unmarshal round advance: %w", err)
	}
	reason, suspect, err := parseRoundAdvanceReason(event.Reason)
	if err != nil {
		return fmt.Errorf("round advance reason: %w", err)
	}
	if reason != "tc" {
		return nil
	}
	if suspect == "" {
		suspect = "unknown"
	}
	metrics.IncrementTimeoutRounds(suspect)
	return nil
}

// parseRoundAdvanceReason accepts both enum encodings hl-node has emitted:
// unit variants as JSON strings ("Qc", "timeout"), and data-bearing
// variants as externally tagged objects ({"Tc": {"suspect": "NoVote", ...}}).
// Returns the bounded reason class and, for Tc, the suspect field when present.
func parseRoundAdvanceReason(raw json.RawMessage) (reason, suspect string, err error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", "", fmt.Errorf("reason is null")
	}
	if raw[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", "", err
		}
		return boundedRoundAdvanceReason(value), "", nil
	}

	var variant map[string]json.RawMessage
	if err := json.Unmarshal(raw, &variant); err != nil {
		return "", "", err
	}
	if len(variant) != 1 {
		return "", "", fmt.Errorf("reason object has %d variants", len(variant))
	}
	for name, payload := range variant {
		reason = boundedRoundAdvanceReason(name)
		var body struct {
			Suspect string `json:"suspect"`
		}
		// payload is opaque beyond the suspect; ignore decode failures
		_ = json.Unmarshal(payload, &body)
		return reason, body.Suspect, nil
	}
	return "", "", fmt.Errorf("reason is invalid")
}

func boundedRoundAdvanceReason(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "qc":
		return "qc"
	case "tc", "timeout":
		return "tc"
	default:
		return "other"
	}
}
