package monitors

import (
	"bytes"
	"encoding/json"
	"errors"
	"time"
)

// unmarshalRequiredJSON rejects JSON null before decoding a required scalar or
// object field. encoding/json otherwise accepts null for primitive destinations
// and silently leaves their zero value, which can turn malformed log records
// into plausible zero observations.
func unmarshalRequiredJSON(raw json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return errors.New("required JSON value is null")
	}
	return json.Unmarshal(trimmed, dst)
}

func rawJSONArray(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '['
}

func rawJSONObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}

// parseVisorTime accepts the timestamp shapes hl-node log streams use
// (RFC3339Nano, RFC3339, plain ISO without zone). Zone-less values are UTC.
func parseVisorTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
