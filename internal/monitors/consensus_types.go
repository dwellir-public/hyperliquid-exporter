package monitors

import (
	"encoding/json"
	"errors"
)

// ConsensusLogEntry represents a parsed consensus log line
type ConsensusLogEntry struct {
	Timestamp string          `json:"-"`
	Direction string          `json:"-"`
	Message   json.RawMessage `json:"-"`
}

// vote message in consensus logs
type VoteMessage struct {
	Vote struct {
		Validator string  `json:"validator"`
		Round     float64 `json:"round"`
	} `json:"vote"`
}

// block message in consensus logs
type BlockMessage struct {
	Round    float64         `json:"round"`
	Proposer string          `json:"proposer"`
	QC       json.RawMessage `json:"qc"`
	TC       json.RawMessage `json:"tc"`
}

// quorum certificate data
type QCData struct {
	Round   float64  `json:"round"`
	Signers []string `json:"signers"`
}

// timeout certificate data
type TCData struct {
	Timeouts []struct {
		Validator string `json:"validator"`
	} `json:"timeouts"`
}

// heartbeat message
type HeartbeatMessage struct {
	Validator string `json:"validator"`
	RandomID  uint64 `json:"random_id"`
	Round     uint64 `json:"round"`
}

// heartbeat acknowledgment
// Validator names the responder on current builds and the origin on older
// ones, often abbreviated (0x1337..334f); the wrapper's sender/source carries
// the full responder identity, so correlation uses that instead.
type HeartbeatAckMessage struct {
	Validator string `json:"validator"`
	RandomID  uint64 `json:"random_id"`
	Round     uint64 `json:"round"`
}

func (h *HeartbeatMessage) UnmarshalJSON(data []byte) error {
	decoded, err := decodeHeartbeat(data)
	if err != nil {
		return err
	}
	*h = decoded
	return nil
}

func (h *HeartbeatAckMessage) UnmarshalJSON(data []byte) error {
	decoded, err := decodeHeartbeat(data)
	if err != nil {
		return err
	}
	*h = HeartbeatAckMessage(decoded)
	return nil
}

type heartbeatRound struct {
	value   uint64
	present bool
}

func (r *heartbeatRound) UnmarshalJSON(data []byte) error {
	if r.present {
		return errors.New("duplicate heartbeat round")
	}
	var value uint64
	if err := unmarshalRequiredJSON(data, &value); err != nil || value == 0 {
		return errors.New("invalid heartbeat round")
	}
	r.value, r.present = value, true
	return nil
}

// decodeHeartbeat reads the round from round or, on builds from October 2026,
// executed_round. Each occurrence is validated so a duplicate key cannot hide a
// malformed or conflicting value. Older builds send neither; their round stays
// zero and the ack joins on random ID alone.
func decodeHeartbeat(data []byte) (HeartbeatMessage, error) {
	var wire struct {
		Validator     string         `json:"validator"`
		RandomID      uint64         `json:"random_id"`
		Round         heartbeatRound `json:"round"`
		ExecutedRound heartbeatRound `json:"executed_round"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return HeartbeatMessage{}, err
	}
	var round uint64
	for _, field := range []heartbeatRound{wire.Round, wire.ExecutedRound} {
		if !field.present {
			continue
		}
		if round != 0 && round != field.value {
			return HeartbeatMessage{}, errors.New("conflicting heartbeat rounds")
		}
		round = field.value
	}
	return HeartbeatMessage{Validator: wire.Validator, RandomID: wire.RandomID, Round: round}, nil
}

// parsed status log line
type StatusLogEntry struct {
	Timestamp              string          `json:"-"`
	DisconnectedValidators json.RawMessage `json:"disconnected_validators"`
	HeartbeatStatuses      json.RawMessage `json:"heartbeat_statuses"`
}

// round advance event data
type RoundAdvancePayload struct {
	Reason  string `json:"reason"`
	Suspect string `json:"suspect"`
}
