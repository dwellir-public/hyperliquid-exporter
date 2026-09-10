package monitors

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadLastLine(t *testing.T) {
	t.Run("multiple lines", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.log")
		if err := os.WriteFile(path, []byte("line1\nline2\nline3\n"), 0644); err != nil {
			t.Fatal(err)
		}

		got, err := ReadLastLine(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "line3" {
			t.Errorf("got %q, want %q", got, "line3")
		}
	})

	t.Run("single line", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.log")
		if err := os.WriteFile(path, []byte("only\n"), 0644); err != nil {
			t.Fatal(err)
		}

		got, err := ReadLastLine(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "only" {
			t.Errorf("got %q, want %q", got, "only")
		}
	})

	t.Run("empty file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.log")
		if err := os.WriteFile(path, []byte{}, 0644); err != nil {
			t.Fatal(err)
		}

		got, err := ReadLastLine(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "" {
			t.Errorf("got %q, want empty", got)
		}
	})

	t.Run("no trailing newline", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "test.log")
		if err := os.WriteFile(path, []byte("line1\nline2"), 0644); err != nil {
			t.Fatal(err)
		}

		got, err := ReadLastLine(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "line2" {
			t.Errorf("got %q, want %q", got, "line2")
		}
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := ReadLastLine("/nonexistent/path")
		if err == nil {
			t.Error("expected error for missing file")
		}
	})
}

func TestProcessValidatorStatusLine(t *testing.T) {
	initTestMetrics(t)
	t.Run("valid status with stakes", func(t *testing.T) {
		line := `["2025-01-01T00:00:00Z", {"home_validator":"0xsigner123","round":42,"current_stakes":[["0xvalidator1","0xsigner1"],["0xvalidator2","0xsigner2"]]}]`
		err := processValidatorStatusLine(line)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("valid status no home validator", func(t *testing.T) {
		line := `["2025-01-01T00:00:00Z", {"home_validator":"","round":10,"current_stakes":[]}]`
		err := processValidatorStatusLine(line)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("malformed JSON", func(t *testing.T) {
		err := processValidatorStatusLine("not-json")
		if err == nil {
			t.Error("expected error for malformed JSON")
		}
	})

	t.Run("wrong array length", func(t *testing.T) {
		err := processValidatorStatusLine(`["only-one"]`)
		if err == nil {
			t.Error("expected error for wrong array length")
		}
	})

	t.Run("invalid data element", func(t *testing.T) {
		err := processValidatorStatusLine(`["ts", "not-an-object"]`)
		if err == nil {
			t.Error("expected error for invalid data element")
		}
	})
}

func TestCurrentStakesUnmarshal(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		wantRows int
		wantErr  bool
	}{
		{"legacy array", `[["0xval1","0xsig1"],["0xval2","0xsig2"]]`, 2, false},
		{"wrapped object", `{"validator_to_stake":[["0xsig1",100.5],["0xsig2",7]]}`, 2, false},
		{"null", `null`, 0, false},
		{"empty array", `[]`, 0, false},
		{"malformed", `{"validator_to_stake":"nope"}`, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var c currentStakes
			err := json.Unmarshal([]byte(tt.raw), &c)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if len(c) != tt.wantRows {
				t.Errorf("rows = %d, want %d", len(c), tt.wantRows)
			}
		})
	}
}

func TestRegisterStakeRows(t *testing.T) {
	initTestMetrics(t)
	legacy := currentStakes{{"0xVal1", "0xSig1"}, {"0xval2", "0xsig2"}, {"short"}}
	if pairs := registerStakeRows(legacy); len(pairs) != 2 || pairs["0xsig1"] != "0xval1" {
		t.Errorf("legacy pairs = %v", pairs)
	}
	modern := currentStakes{{"0xsig1", 100.5}, {"0xsig2", float64(7)}}
	if pairs := registerStakeRows(modern); len(pairs) != 0 {
		t.Errorf("modern rows must not yield signer pairs, got %v", pairs)
	}
}

func TestProcessValidatorStatusLineWrappedStakes(t *testing.T) {
	initTestMetrics(t)
	line := `["2026-09-01T00:00:00Z", {"home_validator":"0xsigner1","round":42,"current_stakes":{"validator_to_stake":[["0xsigner1",100],["0xsigner2",50]]}}]`
	if err := processValidatorStatusLine(line); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestReadLastLineOver64KiB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status")
	long := strings.Repeat("x", 100*1024)
	if err := os.WriteFile(path, []byte("first\n"+long+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLastLine(path)
	if err != nil {
		t.Fatalf("ReadLastLine: %v", err)
	}
	if got != long {
		t.Errorf("got %d bytes, want %d", len(got), len(long))
	}
}
