package goagent

import (
	"context"
	"encoding/json"
	"fmt"
)

// NewTool adapts a supported Go function into a Tool.
//
// The first supported function shape is func(context.Context, string) (string,
// error), which covers the README quick-start case without pretending to be a
// general schema generator.
func NewTool(name, description string, fn any) (Tool, error) {
	if err := validateToolName(name); err != nil {
		return nil, err
	}

	typed, ok := fn.(func(context.Context, string) (string, error))
	if !ok || typed == nil {
		return nil, fmt.Errorf("goagent: tool %q function must have signature func(context.Context, string) (string, error)", name)
	}

	return functionTool{name: name, description: description, fn: typed}, nil
}

type functionTool struct {
	name        string
	description string
	fn          func(context.Context, string) (string, error)
}

func (t functionTool) Name() string { return t.name }

func (t functionTool) Description() string { return t.description }

func (t functionTool) Schema() ToolSchema {
	return ToolSchema{
		"type": "object",
		"additionalProperties": map[string]any{
			"type": "string",
		},
		"minProperties": 1,
		"maxProperties": 1,
	}
}

func (t functionTool) Call(ctx context.Context, call ToolCall) (ToolResult, error) {
	if call.Name != t.name {
		return ToolResult{}, fmt.Errorf("goagent: tool %q cannot execute call for %q", t.name, call.Name)
	}

	input, err := decodeSingleStringInput(t.name, call.Input)
	if err != nil {
		return ToolResult{}, err
	}

	content, err := t.fn(ctx, input)
	if err != nil {
		return ToolResult{}, fmt.Errorf("goagent: tool %q failed: %w", t.name, err)
	}

	return ToolResult{CallID: call.ID, Name: call.Name, Content: content}, nil
}

func validateToolName(name string) error {
	if name == "" {
		return fmt.Errorf("goagent: tool name cannot be empty")
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("goagent: tool name %q contains invalid character %q", name, r)
	}
	return nil
}

func decodeSingleStringInput(toolName string, raw json.RawMessage) (string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", fmt.Errorf("goagent: tool %q input must be a JSON object with one string field: %w", toolName, err)
	}
	if len(object) != 1 {
		return "", fmt.Errorf("goagent: tool %q input must contain exactly one string field", toolName)
	}

	for _, valueRaw := range object {
		var value string
		if err := json.Unmarshal(valueRaw, &value); err != nil {
			return "", fmt.Errorf("goagent: tool %q input value must be a string: %w", toolName, err)
		}
		return value, nil
	}

	panic("unreachable: map length checked above")
}
