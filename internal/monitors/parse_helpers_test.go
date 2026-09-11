package monitors

import (
	"encoding/json"
	"fmt"
	"time"
)

func parseStageProbeJSON() error {
	var v map[string]any
	return fmt.Errorf("wrapped: %w", json.Unmarshal([]byte("{nope"), &v))
}

func parseStageProbeTime() error {
	_, err := time.Parse(time.RFC3339, "not a time")
	return fmt.Errorf("wrapped: %w", err)
}
