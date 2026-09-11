package monitors

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/validaoxyz/hyperliquid-exporter/internal/metrics"
)

const critDailyLine = `["2026-05-25T09:54:58.011179114",["2026-05-23T08:24:54.982100058",0,113,4]]`

func critRichDoc(base string, bugs, crits int) string {
	return fmt.Sprintf(`{"start_time":%q,"n_bugs":%d,"n_crits":%d,"code_location_and_stats":[
[{"fln":"/home/ubuntu/hl/code_Mainnet/base/src/gossip_rpc_client.rs","line":59},{"n":7,"is_ignored":false,"first_seen":"2026-05-25T11:16:54.884388206","last_seen":"2026-05-25T11:27:23.146756922","first_msg":"...unexpected rpc response..."}],
[{"fln":"/home/ubuntu/hl/code_Mainnet/base/src/nv_stream.rs","line":198},{"n":3,"is_ignored":true,"first_seen":"2026-05-25T11:17:00","last_seen":"2026-05-25T11:28:00","first_msg":"...reconnecting..."}]
]}`, base, bugs, crits)
}

func TestParseCritGeneration(t *testing.T) {
	gen, err := parseCritGeneration([]byte(critDailyLine))
	if err != nil {
		t.Fatal(err)
	}
	if gen.NBugs != 0 || gen.NCrits != 113 || gen.NLocations != 4 || gen.BaseTime.Year() != 2026 {
		t.Fatalf("got %+v", gen)
	}
	for _, bad := range []string{
		`["2026-08-08T00:00:00",["2026-08-08T00:00:00",null,0,0]]`,
		`["2026-08-08T00:00:00",["2026-08-08T00:00:00",0,-1,0]]`,
		`["2026-08-08T00:00:00",[0,0,0]]`,
		`{"n_bugs":0}`,
	} {
		if _, err := parseCritGeneration([]byte(bad)); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
}

func TestParseCritLocationsUsesBasenameAndSortsByCount(t *testing.T) {
	base, bugs, crits, locs, err := parseCritLocations([]byte(critRichDoc("2026-05-25T11:12:20.656667039", 0, 10)))
	if err != nil {
		t.Fatal(err)
	}
	if base.IsZero() || bugs != 0 || crits != 10 || len(locs) != 2 {
		t.Fatalf("header %v %d %d, %d locs", base, bugs, crits, len(locs))
	}
	if locs[0].File != "gossip_rpc_client.rs" || locs[0].Line != 59 || locs[0].N != 7 || locs[0].IsIgnored {
		t.Fatalf("first location %+v", locs[0])
	}
	if locs[1].File != "nv_stream.rs" || !locs[1].IsIgnored || locs[1].LastSeen.IsZero() {
		t.Fatalf("second location %+v", locs[1])
	}
	if _, _, _, _, err := parseCritLocations([]byte(`{"start_time":"2026-05-25T11:12:20"}`)); err == nil {
		t.Fatal("accepted a document without counts and locations")
	}
}

func TestCritMsgMonitorPublishesAndWithdraws(t *testing.T) {
	initTestMetrics(t)
	root := t.TempDir()
	rich := filepath.Join(t.TempDir(), "hl-visor.json")
	visorDir := filepath.Join(root, "hl-visor")
	if err := os.MkdirAll(visorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sample := time.Date(2026, 5, 25, 9, 54, 58, 0, time.UTC)
	if err := os.WriteFile(filepath.Join(visorDir, "20260525"), []byte(critDailyLine+"\n"+`["2026-05-25T09:59:58.0",["2026-05-23T08:24:54.982100058",1,120,5]]`+"\n["), 0o644); err != nil {
		t.Fatal(err)
	}
	// rich document from the same generation: same base time and counts
	if err := os.WriteFile(rich, []byte(critRichDoc("2026-05-23T08:24:54.982100058", 1, 120)), 0o644); err != nil {
		t.Fatal(err)
	}

	m := newCritMsgMonitor(root, rich)
	now := sample.Add(6 * time.Minute)
	m.tick(now)

	visor := attribute.String("source", "hl-visor")
	if v, ok := metrics.GaugeSeriesValue(metrics.HLNodeCrits, visor); !ok || v != 120 {
		t.Fatalf("hl_node_crits{hl-visor} = %v %v, want 120 from the last complete line", v, ok)
	}
	if v, ok := metrics.GaugeSeriesValue(metrics.HLNodeBugs, visor); !ok || v != 1 {
		t.Fatalf("hl_node_bugs{hl-visor} = %v %v", v, ok)
	}
	if _, ok := metrics.GaugeSeriesValue(metrics.HLNodeCrits, attribute.String("source", "hl-node")); ok {
		t.Fatal("hl-node series published without a directory")
	}
	loc := []attribute.KeyValue{attribute.String("file", "gossip_rpc_client.rs"), attribute.String("line", "59")}
	if v, ok := metrics.GaugeSeriesValue(metrics.HLNodeCritLocation, loc...); !ok || v != 7 {
		t.Fatalf("hl_node_crit_location = %v %v", v, ok)
	}

	// the visor restarts: the rich document now describes a new generation
	// the daily file has not recorded yet, so locations are withdrawn
	if err := os.WriteFile(rich, []byte(critRichDoc("2026-05-25T10:00:00", 0, 0)), 0o644); err != nil {
		t.Fatal(err)
	}
	m.tick(now)
	if _, ok := metrics.GaugeSeriesValue(metrics.HLNodeCritLocation, loc...); ok {
		t.Fatal("locations kept across a generation mismatch")
	}

	// the daily record goes stale: counters are withdrawn too
	m.tick(now.Add(critMsgStaleAfter))
	if _, ok := metrics.GaugeSeriesValue(metrics.HLNodeCrits, visor); ok {
		t.Fatal("stale hl-visor counters not withdrawn")
	}
}
