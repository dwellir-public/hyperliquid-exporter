package monitors

import (
	"context"
	"testing"

	"github.com/validaoxyz/hyperliquid-exporter/internal/cache"
)

func initBlockGlobals(t *testing.T) {
	t.Helper()
	initTestMetrics(t)
	lastBlockTimes = cache.NewLRUCache(10, 0)
	blockHeightsByState = cache.NewLRUCache(10, 0)
	t.Cleanup(func() {
		lastBlockTimes = cache.NewLRUCache(10, 0)
		blockHeightsByState = cache.NewLRUCache(10, 0)
	})
}

func TestParseBlockTimeLine(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		line      string
		stateType string
		wantErr   bool
	}{
		{
			name:      "valid fast block",
			line:      `{"height":1000,"block_time":"2025-01-01T00:00:01.000000000","apply_duration":0.005,"begin_block_wall_time":"2025-01-01T00:00:00.995"}`,
			stateType: "fast",
			wantErr:   false,
		},
		{
			name:      "valid slow block",
			line:      `{"height":999,"block_time":"2025-01-01T00:00:00.500000000","apply_duration":0.010}`,
			stateType: "slow",
			wantErr:   false,
		},
		{
			name:      "missing height",
			line:      `{"block_time":"2025-01-01T00:00:01.000","apply_duration":0.005}`,
			stateType: "fast",
			wantErr:   true,
		},
		{
			name:      "missing block_time",
			line:      `{"height":1000,"apply_duration":0.005}`,
			stateType: "fast",
			wantErr:   true,
		},
		{
			name:      "missing apply_duration",
			line:      `{"height":1000,"block_time":"2025-01-01T00:00:01.000"}`,
			stateType: "fast",
			wantErr:   true,
		},
		{
			name:      "malformed JSON",
			line:      `{broken`,
			stateType: "fast",
			wantErr:   true,
		},
		{
			name:      "empty string",
			line:      ``,
			stateType: "fast",
			wantErr:   true,
		},
		{
			name:      "invalid block_time format",
			line:      `{"height":1000,"block_time":"not-a-time","apply_duration":0.005}`,
			stateType: "fast",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initBlockGlobals(t)
			err := parseBlockTimeLine(ctx, tt.line, tt.stateType)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseBlockTimeLine() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestParseBlockTimeLine_BlockTimeDiff(t *testing.T) {
	ctx := context.Background()
	initBlockGlobals(t)

	// first block sets the baseline
	line1 := `{"height":100,"block_time":"2025-01-01T00:00:00.000000000","apply_duration":0.001}`
	if err := parseBlockTimeLine(ctx, line1, "fast"); err != nil {
		t.Fatalf("first block: %v", err)
	}

	// second block should compute a diff without error
	line2 := `{"height":101,"block_time":"2025-01-01T00:00:01.000000000","apply_duration":0.001}`
	if err := parseBlockTimeLine(ctx, line2, "fast"); err != nil {
		t.Fatalf("second block: %v", err)
	}

	// verify block heights are tracked
	heightsIface, exists := blockHeightsByState.Get("fast")
	if !exists {
		t.Fatal("block heights not stored")
	}
	heights := heightsIface.([]int64)
	if len(heights) != 2 {
		t.Errorf("expected 2 heights, got %d", len(heights))
	}
}

func TestParseLegacyBlockTimeLine(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		line    string
		wantErr bool
	}{
		{
			name:    "valid legacy line",
			line:    `{"height":500,"block_time":"2025-01-01T00:00:00.000000000","apply_duration":0.003}`,
			wantErr: false,
		},
		{
			name:    "malformed JSON",
			line:    `{broken`,
			wantErr: true,
		},
		{
			name:    "missing required fields",
			line:    `{"height":500}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initBlockGlobals(t)
			err := parseLegacyBlockTimeLine(ctx, tt.line)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseLegacyBlockTimeLine() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
