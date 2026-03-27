package monitors

import (
	"context"
	"testing"
)

func TestParseProposalLine(t *testing.T) {
	initTestMetrics(t)
	ctx := context.Background()

	tests := []struct {
		name    string
		line    string
		wantErr bool
	}{
		{
			name:    "valid proposal",
			line:    `{"abci_block":{"proposer":"0xabc123def456"}}`,
			wantErr: false,
		},
		{
			name:    "missing proposer field",
			line:    `{"abci_block":{}}`,
			wantErr: true,
		},
		{
			name:    "missing abci_block",
			line:    `{"other":"data"}`,
			wantErr: true,
		},
		{
			name:    "non-JSON line skipped",
			line:    `some plain text log line`,
			wantErr: false,
		},
		{
			name:    "empty line skipped",
			line:    ``,
			wantErr: false,
		},
		{
			name:    "array line silently skipped",
			line:    `["timestamp","data"]`,
			wantErr: false, // starts with '[', unmarshal into map fails, returns nil
		},
		{
			name:    "proposer is not a string",
			line:    `{"abci_block":{"proposer":12345}}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := parseProposalLine(ctx, tt.line)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseProposalLine() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
