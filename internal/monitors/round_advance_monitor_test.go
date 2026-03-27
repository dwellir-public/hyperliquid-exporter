package monitors

import (
	"testing"
)

func TestParseRoundAdvanceLine(t *testing.T) {
	initTestMetrics(t)
	tests := []struct {
		name    string
		line    string
		wantErr bool
	}{
		{
			name:    "valid timeout with suspect",
			line:    `["round_advance", {"reason":"timeout","suspect":"0xabc123"}]`,
			wantErr: false,
		},
		{
			name:    "timeout without suspect defaults to unknown",
			line:    `["round_advance", {"reason":"timeout"}]`,
			wantErr: false,
		},
		{
			name:    "non-timeout reason ignored",
			line:    `["round_advance", {"reason":"qc"}]`,
			wantErr: false,
		},
		{
			name:    "wrong event name ignored",
			line:    `["other_event", {"reason":"timeout"}]`,
			wantErr: false,
		},
		{
			name:    "malformed JSON",
			line:    `not-json`,
			wantErr: true,
		},
		{
			name:    "too few elements",
			line:    `["round_advance"]`,
			wantErr: true,
		},
		{
			name:    "payload not object",
			line:    `["round_advance", "string"]`,
			wantErr: true,
		},
		{
			name:    "first element not string",
			line:    `[123, {"reason":"timeout"}]`,
			wantErr: false, // returns nil (not a round_advance)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parseRoundAdvanceLine(tt.line)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseRoundAdvanceLine() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
