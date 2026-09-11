package metrics

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetPublicIPFromFile(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"json string": {`"203.0.113.7"`, "203.0.113.7"},
		"bare string": {"203.0.113.8\n", "203.0.113.8"},
		"not an ip":   {`{"ip":"1.2.3.4"}`, ""},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.WriteFile(filepath.Join(home, "last_known_public_ip.json"), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				t.Skip("falls through to network lookup")
			}
			if got := getPublicIP(home); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
