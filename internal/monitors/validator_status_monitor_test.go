package monitors

import (
	"os"
	"path/filepath"
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
