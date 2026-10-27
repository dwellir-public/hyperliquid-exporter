package monitors

import (
	"encoding/json"
	"testing"
)

// mapRawJSONObject is the decode-into-map check rawJSONObject replaced; the
// two must agree on every input.
func mapRawJSONObject(raw json.RawMessage) bool {
	var object map[string]json.RawMessage
	return json.Unmarshal(raw, &object) == nil && object != nil
}

func TestRawJSONObject(t *testing.T) {
	for raw, want := range map[string]bool{
		`{}`:                       true,
		` {"ip":"1.2.3.4"}` + "\n": true,
		`{"k":1,"k":2}`:            true,
		``:                         false,
		`null`:                     false,
		`[]`:                       false,
		`"{}"`:                     false,
		`{"k":}`:                   false,
		`{} {}`:                    false,
		"\v{}":                     false,
		" {}":                 false,
	} {
		if got := rawJSONObject([]byte(raw)); got != want {
			t.Errorf("rawJSONObject(%q) = %v, want %v", raw, got, want)
		}
		if got := mapRawJSONObject([]byte(raw)); got != want {
			t.Errorf("mapRawJSONObject(%q) = %v, want %v", raw, got, want)
		}
	}
}

func FuzzRawJSONObject(f *testing.F) {
	for _, raw := range []string{``, `{}`, `null`, `[]`, `{"k":null}`, `{"k":1,"k":2}`, " {}", "{\"\xff\":1}"} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		if got, want := rawJSONObject(raw), mapRawJSONObject(raw); got != want {
			t.Fatalf("rawJSONObject(%q) = %v, map decode = %v", raw, got, want)
		}
	})
}
