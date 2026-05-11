package goagent

import (
	"encoding/json"
	"fmt"
)

// ValidateToolResult reports whether r can be serialized for events, session
// persistence, and JSON replay. JSON and Opaque must marshal with
// encoding/json; other fields are plain strings, integers, or booleans.
func ValidateToolResult(r ToolResult) error {
	if _, err := json.Marshal(r.JSON); err != nil {
		return fmt.Errorf("goagent: ToolResult.JSON: %w", err)
	}
	if r.Opaque != nil {
		if _, err := json.Marshal(r.Opaque); err != nil {
			return fmt.Errorf("goagent: ToolResult.Opaque: %w", err)
		}
	}
	return nil
}
