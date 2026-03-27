package replica

import (
	"encoding/json"
	"testing"
	"time"
)

// --- countJSONArrayElements ---

func TestCountEmpty(t *testing.T) {
	// starts at count=1, no commas found → returns 1
	if got := countJSONArrayElements(json.RawMessage(`[]`)); got != 1 {
		t.Fatalf("got %d, want 1", got)
	}
}

func TestCountSingle(t *testing.T) {
	if got := countJSONArrayElements(json.RawMessage(`[{"id":1}]`)); got != 1 {
		t.Fatalf("got %d, want 1", got)
	}
}

func TestCountMultiple(t *testing.T) {
	if got := countJSONArrayElements(json.RawMessage(`[1,2,3]`)); got != 3 {
		t.Fatalf("got %d, want 3", got)
	}
}

func TestCountNested(t *testing.T) {
	// nested commas at depth>1 should not be counted
	if got := countJSONArrayElements(json.RawMessage(`[1,[2,3],4]`)); got != 3 {
		t.Fatalf("got %d, want 3", got)
	}
}

func TestCountEscapedComma(t *testing.T) {
	if got := countJSONArrayElements(json.RawMessage(`["a,b","c"]`)); got != 2 {
		t.Fatalf("got %d, want 2", got)
	}
}

func TestCountEscapedQuote(t *testing.T) {
	if got := countJSONArrayElements(json.RawMessage(`["a\"b","c"]`)); got != 2 {
		t.Fatalf("got %d, want 2", got)
	}
}

func TestCountNestedObjects(t *testing.T) {
	// depth tracking only counts [] brackets, so commas inside {} at depth 1
	// are still counted — this means objects with internal commas inflate the count.
	// The function is documented as "approximate but much faster than parsing".
	data := `[{"a":1,"b":2},{"c":3}]`
	got := countJSONArrayElements(json.RawMessage(data))
	// 2 top-level commas between elements + 1 comma inside first object = 3 depth-1 commas
	// actual elements = 2, but function returns 3 due to approximation
	if got != 3 {
		t.Fatalf("got %d, want 3 (approximate count)", got)
	}
}

// --- ExtractMetrics ---

func TestExtractBasic(t *testing.T) {
	p := NewParser(1)
	block := &ReplicaBlock{}
	block.ABCIBlock.Time = "2024-06-15T12:30:00.123456789Z"
	block.ABCIBlock.Round = 42
	block.ABCIBlock.Proposer = "0xabc"

	m, err := p.ExtractMetrics(block)
	if err != nil {
		t.Fatal(err)
	}
	if m.Round != 42 {
		t.Errorf("Round = %d, want 42", m.Round)
	}
	if m.Proposer != "0xabc" {
		t.Errorf("Proposer = %q, want 0xabc", m.Proposer)
	}
	if m.TotalActions != 0 {
		t.Errorf("TotalActions = %d, want 0", m.TotalActions)
	}
	if m.TotalOperations != 0 {
		t.Errorf("TotalOperations = %d, want 0", m.TotalOperations)
	}
}

func TestExtractTimeRFC3339(t *testing.T) {
	p := NewParser(1)
	block := &ReplicaBlock{}
	block.ABCIBlock.Time = "2024-01-01T00:00:00.123Z"

	m, err := p.ExtractMetrics(block)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2024, 1, 1, 0, 0, 0, 123000000, time.UTC)
	if !m.Time.Equal(want) {
		t.Errorf("Time = %v, want %v", m.Time, want)
	}
}

func TestExtractTimeFallback(t *testing.T) {
	p := NewParser(1)
	block := &ReplicaBlock{}
	block.ABCIBlock.Time = "2024-01-01T00:00:00.123"

	m, err := p.ExtractMetrics(block)
	if err != nil {
		t.Fatal(err)
	}
	if m.Time.Location() != time.UTC {
		t.Errorf("expected UTC, got %v", m.Time.Location())
	}
}

func TestExtractTimeBad(t *testing.T) {
	p := NewParser(1)
	block := &ReplicaBlock{}
	block.ABCIBlock.Time = "not-a-time"

	_, err := p.ExtractMetrics(block)
	if err == nil {
		t.Fatal("expected error for bad time")
	}
}

func TestExtractWithActions(t *testing.T) {
	p := NewParser(1)
	block := &ReplicaBlock{}
	block.ABCIBlock.Time = "2024-01-01T00:00:00Z"

	// structure: [[hash, bundleData], ...]
	bundles := `[
		["hash1", {
			"signed_actions": [
				{"action": {"type": "order", "orders": [{"o":1},{"o":2}]}},
				{"action": {"type": "cancel", "cancels": [{"c":1}]}},
				{"action": {"type": "evmRawTx"}}
			]
		}]
	]`
	block.ABCIBlock.SignedActionBundles = json.RawMessage(bundles)

	m, err := p.ExtractMetrics(block)
	if err != nil {
		t.Fatal(err)
	}
	if m.TotalActions != 3 {
		t.Errorf("TotalActions = %d, want 3", m.TotalActions)
	}
	if m.ActionCounts[ActionTypeOrder] != 1 {
		t.Errorf("order actions = %d, want 1", m.ActionCounts[ActionTypeOrder])
	}
	if m.OperationCounts[ActionTypeOrder] != 2 {
		t.Errorf("order operations = %d, want 2", m.OperationCounts[ActionTypeOrder])
	}
	if m.OperationCounts[ActionTypeCancel] != 1 {
		t.Errorf("cancel operations = %d, want 1", m.OperationCounts[ActionTypeCancel])
	}
	if m.OperationCounts[ActionTypeEvmRawTx] != 1 {
		t.Errorf("evmRawTx operations = %d, want 1", m.OperationCounts[ActionTypeEvmRawTx])
	}
}

// --- parseActionBundles ---

func TestBundleOperationCounting(t *testing.T) {
	p := NewParser(1)
	bundles := json.RawMessage(`[
		["hash", {
			"signed_actions": [
				{"action": {"type": "order", "orders": [{"o":1},{"o":2},{"o":3}]}}
			]
		}]
	]`)

	actionCounts := make(map[string]int)
	operationCounts := make(map[string]int)
	var totalActions, totalOps int

	if err := p.parseActionBundles(bundles, actionCounts, operationCounts, &totalActions, &totalOps); err != nil {
		t.Fatal(err)
	}
	if actionCounts[ActionTypeOrder] != 1 {
		t.Errorf("action count = %d, want 1", actionCounts[ActionTypeOrder])
	}
	if operationCounts[ActionTypeOrder] != 3 {
		t.Errorf("operation count = %d, want 3", operationCounts[ActionTypeOrder])
	}
}

func TestBundleEmptyType(t *testing.T) {
	p := NewParser(1)
	bundles := json.RawMessage(`[
		["hash", {
			"signed_actions": [
				{"action": {"type": ""}}
			]
		}]
	]`)

	actionCounts := make(map[string]int)
	operationCounts := make(map[string]int)
	var totalActions, totalOps int

	if err := p.parseActionBundles(bundles, actionCounts, operationCounts, &totalActions, &totalOps); err != nil {
		t.Fatal(err)
	}
	if actionCounts[ActionTypeOther] != 1 {
		t.Errorf("expected empty type counted as 'other', got %v", actionCounts)
	}
}

func TestBundleMalformedSkipped(t *testing.T) {
	p := NewParser(1)
	// first pair is invalid JSON, second is valid
	bundles := json.RawMessage(`[
		"not-an-array",
		["hash2", {
			"signed_actions": [
				{"action": {"type": "order", "orders": [{"o":1}]}}
			]
		}]
	]`)

	actionCounts := make(map[string]int)
	operationCounts := make(map[string]int)
	var totalActions, totalOps int

	if err := p.parseActionBundles(bundles, actionCounts, operationCounts, &totalActions, &totalOps); err != nil {
		t.Fatal(err)
	}
	if totalActions != 1 {
		t.Errorf("totalActions = %d, want 1 (malformed should be skipped)", totalActions)
	}
}

// --- ParseBlockFromLine ---

func TestParseValidLine(t *testing.T) {
	p := NewParser(1)
	line := []byte(`{"abci_block":{"time":"2024-01-01T00:00:00Z","round":5,"proposer":"0xabc"}}`)

	block, err := p.ParseBlockFromLine(line)
	if err != nil {
		t.Fatal(err)
	}
	defer p.ReturnBlock(block)

	if block.ABCIBlock.Round != 5 {
		t.Errorf("Round = %d, want 5", block.ABCIBlock.Round)
	}
	if block.ABCIBlock.Proposer != "0xabc" {
		t.Errorf("Proposer = %q, want 0xabc", block.ABCIBlock.Proposer)
	}
}

func TestParseInvalidLine(t *testing.T) {
	p := NewParser(1)
	_, err := p.ParseBlockFromLine([]byte(`{invalid`))
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- Reset ---

func TestResetClearsFields(t *testing.T) {
	block := &ReplicaBlock{}
	block.ABCIBlock.Time = "some-time"
	block.ABCIBlock.Round = 99
	block.ABCIBlock.Proposer = "0xabc"
	block.ABCIBlock.SignedActionBundles = json.RawMessage(`[1,2,3]`)

	block.Reset()

	if block.ABCIBlock.Time != "" {
		t.Errorf("Time not cleared: %q", block.ABCIBlock.Time)
	}
	if block.ABCIBlock.Round != 0 {
		t.Errorf("Round not cleared: %d", block.ABCIBlock.Round)
	}
	if block.ABCIBlock.Proposer != "" {
		t.Errorf("Proposer not cleared: %q", block.ABCIBlock.Proposer)
	}
	if block.ABCIBlock.SignedActionBundles != nil {
		t.Error("SignedActionBundles not cleared")
	}
}
