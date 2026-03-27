package abci

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vmihailenco/msgpack/v5"
)

func writeMsgpackFile(t *testing.T, path string, data any) {
	t.Helper()
	b, err := msgpack.Marshal(data)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, b, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// fixture structs mirror the reader's expected msgpack layout

type fixtureContext struct {
	Exchange struct {
		Context struct {
			Height     int64  `msgpack:"height"`
			TxIndex    int64  `msgpack:"tx_index"`
			Time       string `msgpack:"time"`
			NextOid    int64  `msgpack:"next_oid"`
			NextLid    int64  `msgpack:"next_lid"`
			NextTwapId int64  `msgpack:"next_twap_id"`
			Hardfork   struct {
				Version int64 `msgpack:"version"`
			} `msgpack:"hardfork"`
		} `msgpack:"context"`
	} `msgpack:"exchange"`
}

type fixtureValidatorProfiles struct {
	Exchange struct {
		Consensus struct {
			ValidatorToProfile [][]any `msgpack:"validator_to_profile"`
		} `msgpack:"consensus"`
	} `msgpack:"exchange"`
}

func TestReadContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.msgpack")

	var fixture fixtureContext
	fixture.Exchange.Context.Height = 12345
	fixture.Exchange.Context.TxIndex = 7
	fixture.Exchange.Context.Time = "2024-06-15T12:00:00Z"
	fixture.Exchange.Context.NextOid = 100
	fixture.Exchange.Context.NextLid = 200
	fixture.Exchange.Context.NextTwapId = 300
	fixture.Exchange.Context.Hardfork.Version = 5

	writeMsgpackFile(t, path, &fixture)

	r := NewReader(1)
	ctx, err := r.ReadContext(path)
	if err != nil {
		t.Fatal(err)
	}

	if ctx.Height != 12345 {
		t.Errorf("Height = %d, want 12345", ctx.Height)
	}
	if ctx.TxIndex != 7 {
		t.Errorf("TxIndex = %d, want 7", ctx.TxIndex)
	}
	if ctx.Time != "2024-06-15T12:00:00Z" {
		t.Errorf("Time = %q", ctx.Time)
	}
	if ctx.NextOid != 100 {
		t.Errorf("NextOid = %d, want 100", ctx.NextOid)
	}
	if ctx.NextLid != 200 {
		t.Errorf("NextLid = %d, want 200", ctx.NextLid)
	}
	if ctx.NextTwapId != 300 {
		t.Errorf("NextTwapId = %d, want 300", ctx.NextTwapId)
	}
	if ctx.HardforkVersion != 5 {
		t.Errorf("HardforkVersion = %d, want 5", ctx.HardforkVersion)
	}
}

func TestReadContextCaching(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.msgpack")

	var fixture fixtureContext
	fixture.Exchange.Context.Height = 1
	writeMsgpackFile(t, path, &fixture)

	r := NewReader(1)

	ctx1, err := r.ReadContext(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx2, err := r.ReadContext(path)
	if err != nil {
		t.Fatal(err)
	}

	// same pointer means cache hit
	if ctx1 != ctx2 {
		t.Error("second call should return cached pointer")
	}
}

func TestReadContextMissing(t *testing.T) {
	r := NewReader(1)
	_, err := r.ReadContext("/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadValidatorProfiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.msgpack")

	fixture := fixtureValidatorProfiles{}
	fixture.Exchange.Consensus.ValidatorToProfile = [][]any{
		{
			"0xvalidator1",
			map[string]any{
				"name":    "node-alpha",
				"node_ip": map[string]any{"Ip": "10.0.0.1"},
			},
		},
		{
			"0xvalidator2",
			map[string]any{
				"name":    "node-beta",
				"node_ip": map[string]any{"Ip": "10.0.0.2"},
			},
		},
	}

	writeMsgpackFile(t, path, &fixture)

	r := NewReader(1)
	profiles, err := r.ReadValidatorProfiles(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(profiles) != 2 {
		t.Fatalf("got %d profiles, want 2", len(profiles))
	}
	if profiles[0].Address != "0xvalidator1" || profiles[0].Moniker != "node-alpha" || profiles[0].IP != "10.0.0.1" {
		t.Errorf("profile[0] = %+v", profiles[0])
	}
	if profiles[1].Address != "0xvalidator2" || profiles[1].Moniker != "node-beta" || profiles[1].IP != "10.0.0.2" {
		t.Errorf("profile[1] = %+v", profiles[1])
	}
}

func TestReadValidatorProfilesSkipsIncomplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.msgpack")

	fixture := fixtureValidatorProfiles{}
	fixture.Exchange.Consensus.ValidatorToProfile = [][]any{
		// missing IP
		{
			"0xval1",
			map[string]any{"name": "has-name"},
		},
		// missing moniker
		{
			"0xval2",
			map[string]any{"node_ip": map[string]any{"Ip": "10.0.0.1"}},
		},
		// complete
		{
			"0xval3",
			map[string]any{
				"name":    "complete",
				"node_ip": map[string]any{"Ip": "10.0.0.3"},
			},
		},
	}

	writeMsgpackFile(t, path, &fixture)

	r := NewReader(1)
	profiles, err := r.ReadValidatorProfiles(path)
	if err != nil {
		t.Fatal(err)
	}

	if len(profiles) != 1 {
		t.Fatalf("got %d profiles, want 1 (incomplete entries should be skipped)", len(profiles))
	}
	if profiles[0].Address != "0xval3" {
		t.Errorf("expected 0xval3, got %s", profiles[0].Address)
	}
}
