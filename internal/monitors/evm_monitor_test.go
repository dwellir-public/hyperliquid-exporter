package monitors

import (
	"testing"
	"time"
)

func resetEVMGlobals(t *testing.T) {
	t.Helper()
	initTestMetrics(t)
	orig := lastEVMBlockTime
	lastEVMBlockTime = time.Time{}
	t.Cleanup(func() { lastEVMBlockTime = orig })
}

func TestProcessBlockData(t *testing.T) {
	resetEVMGlobals(t)

	// minimal valid block structure with standard gas limit (<=2M)
	standardBlock := map[string]any{
		"block": map[string]any{
			"Standard": map[string]any{
				"header": map[string]any{
					"header": map[string]any{
						"number":        "0x64",     // 100
						"gasLimit":      "0x1e8480", // 2_000_000
						"gasUsed":       "0xf4240",  // 1_000_000
						"timestamp":     "0x6789abcd",
						"baseFeePerGas": "0x3b9aca00", // 1 gwei
					},
				},
				"body": map[string]any{
					"transactions": []any{},
				},
			},
		},
	}

	ts := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	blockType, err := processBlockData(standardBlock, ts)
	if err != nil {
		t.Fatalf("processBlockData() error: %v", err)
	}

	// blockType depends on blockTypeMetricsEnabled global
	// with it disabled (default), blockType should be empty
	if blockTypeMetricsEnabled && blockType != "standard" {
		t.Errorf("expected blockType 'standard', got %q", blockType)
	}
	if !blockTypeMetricsEnabled && blockType != "" {
		t.Errorf("expected empty blockType when disabled, got %q", blockType)
	}
}

func TestProcessBlockData_HighGas(t *testing.T) {
	resetEVMGlobals(t)

	// temporarily enable block type metrics
	orig := blockTypeMetricsEnabled
	blockTypeMetricsEnabled = true
	t.Cleanup(func() { blockTypeMetricsEnabled = orig })

	highGasBlock := map[string]any{
		"block": map[string]any{
			"High": map[string]any{
				"header": map[string]any{
					"header": map[string]any{
						"number":   "0xc8",      // 200
						"gasLimit": "0x1c9c380", // 30_000_000
						"gasUsed":  "0xe4e1c0",  // 15_000_000
					},
				},
				"body": map[string]any{
					"transactions": []any{},
				},
			},
		},
	}

	blockType, err := processBlockData(highGasBlock, time.Time{})
	if err != nil {
		t.Fatalf("processBlockData() error: %v", err)
	}
	if blockType != "high" {
		t.Errorf("expected blockType 'high', got %q", blockType)
	}
}

func TestProcessBlockData_InvalidInput(t *testing.T) {
	resetEVMGlobals(t)

	tests := []struct {
		name  string
		input any
	}{
		{"not a map", "string"},
		{"missing block field", map[string]any{"other": "data"}},
		{"block not a map", map[string]any{"block": "string"}},
		{"no valid block content", map[string]any{"block": map[string]any{"key": "string"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := processBlockData(tt.input, time.Time{})
			if err == nil {
				t.Error("expected error")
			}
		})
	}
}

func TestProcessTransactions(t *testing.T) {
	tests := []struct {
		name      string
		body      map[string]any
		blockType string
		wantErr   bool
	}{
		{
			name:      "no transactions field",
			body:      map[string]any{},
			blockType: "",
			wantErr:   false,
		},
		{
			name: "eip1559 transaction",
			body: map[string]any{
				"transactions": []any{
					map[string]any{
						"transaction": map[string]any{
							"Eip1559": map[string]any{
								"to":                   "0xabcdef1234567890abcdef1234567890abcdef12",
								"maxPriorityFeePerGas": "0x3b9aca00",
							},
						},
					},
				},
			},
			blockType: "",
			wantErr:   false,
		},
		{
			name: "legacy transaction",
			body: map[string]any{
				"transactions": []any{
					map[string]any{
						"transaction": map[string]any{
							"Legacy": map[string]any{
								"to": "0xabcdef1234567890abcdef1234567890abcdef12",
							},
						},
					},
				},
			},
			blockType: "",
			wantErr:   false,
		},
		{
			name: "contract creation (zero address)",
			body: map[string]any{
				"transactions": []any{
					map[string]any{
						"transaction": map[string]any{
							"Eip1559": map[string]any{
								"to": "0x0000000000000000000000000000000000000000",
							},
						},
					},
				},
			},
			blockType: "",
			wantErr:   false,
		},
		{
			name: "contract creation (empty to)",
			body: map[string]any{
				"transactions": []any{
					map[string]any{
						"transaction": map[string]any{
							"Eip1559": map[string]any{
								"to": "",
							},
						},
					},
				},
			},
			blockType: "",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := processTransactions(tt.body, tt.blockType)
			if (err != nil) != tt.wantErr {
				t.Errorf("processTransactions() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestProcessEVMBlockAndReceiptsLine(t *testing.T) {
	resetEVMGlobals(t)

	tests := []struct {
		name    string
		line    string
		wantErr bool
	}{
		{
			name:    "valid line with block",
			line:    `["2025-01-01T00:00:00.000000000Z", {"block":{"Standard":{"header":{"header":{"number":"0x1","gasLimit":"0x1e8480","gasUsed":"0x0"}},"body":{"transactions":[]}}}}]`,
			wantErr: false,
		},
		{
			name:    "malformed JSON",
			line:    `not-json`,
			wantErr: true,
		},
		{
			name:    "too few elements",
			line:    `["2025-01-01T00:00:00Z"]`,
			wantErr: true,
		},
		{
			name:    "timestamp not a string",
			line:    `[12345, {"block":{}}]`,
			wantErr: true,
		},
		{
			name:    "invalid block data",
			line:    `["2025-01-01T00:00:00Z", "not-a-block"]`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetEVMGlobals(t)
			err := processEVMBlockAndReceiptsLine(tt.line)
			if (err != nil) != tt.wantErr {
				t.Errorf("processEVMBlockAndReceiptsLine() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
