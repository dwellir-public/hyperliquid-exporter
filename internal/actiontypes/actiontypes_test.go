package actiontypes

import "testing"

func TestNormalize(t *testing.T) {
	for raw, want := range map[string]string{
		"order":         "order",
		"outcomeDeploy": "outcomeDeploy",
		"trailingStop":  "trailingStop",
		"":              Other,
		"bogusAction":   Other,
	} {
		got, _ := Normalize(raw)
		if got != want {
			t.Errorf("Normalize(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestCategory(t *testing.T) {
	for action, want := range map[string]string{
		"trailingStop":  "trading",
		"outcomeDeploy": "deployment",
		"order":         "trading",
		"usdSend":       "transfer",
		"noop":          "system",
		Other:           Other,
	} {
		if got := Category(action); got != want {
			t.Errorf("Category(%q) = %q, want %q", action, got, want)
		}
	}
}
