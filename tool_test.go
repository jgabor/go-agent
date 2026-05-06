package goagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	goagent "github.com/jgabor/go-agent"
)

func TestNewToolAdaptsContextStringFunction(t *testing.T) {
	type contextKey string
	const key contextKey = "request-id"

	tool, err := goagent.NewTool("weather", "Get the weather for a city.", func(ctx context.Context, city string) (string, error) {
		if ctx.Value(key) != "request-1" {
			t.Fatalf("tool did not receive context value")
		}
		return "clear in " + city, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if tool.Name() != "weather" {
		t.Fatalf("Name = %q, want weather", tool.Name())
	}
	if tool.Description() == "" {
		t.Fatal("Description is empty")
	}
	if tool.Schema()["type"] != "object" {
		t.Fatalf("Schema type = %v, want object", tool.Schema()["type"])
	}

	result, err := tool.Call(context.WithValue(context.Background(), key, "request-1"), goagent.ToolCall{
		ID:    "call-1",
		Name:  "weather",
		Input: json.RawMessage(`{"city":"Austin"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.CallID != "call-1" || result.Name != "weather" || result.Content != "clear in Austin" {
		t.Fatalf("ToolResult = %+v", result)
	}
}

func TestNewToolAdaptsStructInputFunctionAndGeneratesSchema(t *testing.T) {
	type weatherInput struct {
		City string `json:"city" description:"City name."`
		Days int    `json:"days"`
		Unit string `json:"unit,omitempty"`
		Skip string `json:"-"`
	}

	tool, err := goagent.NewTool("weather", "Get the weather for a city.", func(ctx context.Context, input weatherInput) (string, error) {
		if input.City != "Austin" || input.Days != 2 || input.Unit != "F" {
			t.Fatalf("input = %+v", input)
		}
		return input.City + " forecast", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	schema := tool.Schema()
	if schema["type"] != "object" || schema["additionalProperties"] != false {
		t.Fatalf("Schema = %+v", schema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties = %T, want map[string]any", schema["properties"])
	}
	if _, ok := properties["Skip"]; ok {
		t.Fatalf("ignored field included in schema: %+v", properties)
	}
	city := properties["city"].(map[string]any)
	if city["type"] != "string" || city["description"] != "City name." {
		t.Fatalf("city schema = %+v", city)
	}
	days := properties["days"].(map[string]any)
	if days["type"] != "integer" {
		t.Fatalf("days schema = %+v", days)
	}
	unit := properties["unit"].(map[string]any)
	if unit["type"] != "string" {
		t.Fatalf("unit schema = %+v", unit)
	}
	required := schema["required"].([]string)
	if len(required) != 2 || required[0] != "city" || required[1] != "days" {
		t.Fatalf("required = %+v", required)
	}

	result, err := tool.Call(context.Background(), goagent.ToolCall{
		ID:    "call-1",
		Name:  "weather",
		Input: json.RawMessage(`{"city":"Austin","days":2,"unit":"F"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "Austin forecast" {
		t.Fatalf("Content = %q", result.Content)
	}
}

func TestNewToolWithSchemaUsesExplicitSchema(t *testing.T) {
	explicit := goagent.ToolSchema{
		"type": "object",
		"properties": map[string]any{
			"city": map[string]any{"type": "string", "enum": []string{"Austin"}},
		},
	}
	tool, err := goagent.NewToolWithSchema("weather", "Get weather.", explicit, func(context.Context, string) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	schema := tool.Schema()
	properties := schema["properties"].(map[string]any)
	city := properties["city"].(map[string]any)
	if city["enum"] == nil {
		t.Fatalf("explicit schema not preserved: %+v", schema)
	}
	schema["type"] = "changed"
	if tool.Schema()["type"] != "object" {
		t.Fatal("Schema did not return a top-level copy")
	}

	if _, err := goagent.NewToolWithSchema("weather", "Get weather.", nil, func(context.Context, string) (string, error) {
		return "ok", nil
	}); err == nil {
		t.Fatal("NewToolWithSchema succeeded with nil schema")
	}
}

func TestNewToolReportsSchemaDerivationMismatchPrecisely(t *testing.T) {
	type invalidInput struct {
		Unsupported chan string `json:"unsupported"`
	}

	_, err := goagent.NewTool("weather", "Get weather.", func(context.Context, invalidInput) (string, error) {
		return "ok", nil
	})
	if err == nil {
		t.Fatal("NewTool succeeded with unsupported schema field")
	}
	for _, want := range []string{"tool \"weather\"", "field Unsupported", "unsupported schema type chan string"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error = %q, want %q", err, want)
		}
	}
}

func TestNewToolFromDefinitionCarriesRuntimeMetadata(t *testing.T) {
	tool, err := goagent.NewToolFromDefinition(goagent.ToolDefinition{
		Name:        "weather",
		Description: "Get weather with explicit advanced metadata.",
		Schema: goagent.ToolSchema{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string", "description": "City name."},
			},
			"required": []string{"city"},
		},
		Function: func(context.Context, string) (string, error) {
			return "clear", nil
		},
		Safety: goagent.ToolSafety{ReadOnly: true, Retryable: true},
		Constraints: goagent.ToolConstraints{
			Timeout:        time.Second,
			MaxOutputBytes: 32,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	metadata, ok := tool.(interface{ Metadata() goagent.ToolMetadata })
	if !ok {
		t.Fatal("advanced tool does not expose runtime metadata")
	}
	got := metadata.Metadata()
	if !got.Safety.ReadOnly || !got.Safety.Retryable || got.Constraints.Timeout != time.Second || got.Constraints.MaxOutputBytes != 32 {
		t.Fatalf("Metadata = %+v", got)
	}
	if tool.Schema()["type"] != "object" || tool.Description() == "" {
		t.Fatalf("advanced tool model metadata missing: description=%q schema=%+v", tool.Description(), tool.Schema())
	}
}

func TestNewToolFromDefinitionRejectsInvalidMetadata(t *testing.T) {
	valid := goagent.ToolDefinition{
		Name:        "weather",
		Description: "Get weather.",
		Schema:      goagent.ToolSchema{"type": "object"},
		Function: func(context.Context, string) (string, error) {
			return "ok", nil
		},
	}

	tests := []struct {
		name       string
		mutate     func(*goagent.ToolDefinition)
		wantErr    string
		wantPrefix string
	}{
		{name: "bad name", mutate: func(def *goagent.ToolDefinition) { def.Name = "bad.name" }, wantErr: "invalid character", wantPrefix: "invalid tool definition"},
		{name: "empty description", mutate: func(def *goagent.ToolDefinition) { def.Description = " " }, wantErr: "description cannot be empty", wantPrefix: "invalid tool definition"},
		{name: "nil schema", mutate: func(def *goagent.ToolDefinition) { def.Schema = nil }, wantErr: "schema cannot be nil", wantPrefix: "invalid tool definition"},
		{name: "negative timeout", mutate: func(def *goagent.ToolDefinition) { def.Constraints.Timeout = -time.Second }, wantErr: "timeout cannot be negative", wantPrefix: "invalid tool definition"},
		{name: "negative output", mutate: func(def *goagent.ToolDefinition) { def.Constraints.MaxOutputBytes = -1 }, wantErr: "max output bytes cannot be negative", wantPrefix: "invalid tool definition"},
		{name: "bad function", mutate: func(def *goagent.ToolDefinition) {
			def.Function = func(context.Context) (string, error) { return "", nil }
		}, wantErr: "func(context.Context, string|struct) (string, error)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			definition := valid
			tt.mutate(&definition)
			_, err := goagent.NewToolFromDefinition(definition)
			if err == nil {
				t.Fatal("NewToolFromDefinition succeeded with invalid metadata")
			}
			if tt.wantPrefix != "" && !strings.Contains(err.Error(), tt.wantPrefix) {
				t.Fatalf("error = %q, want prefix %q", err, tt.wantPrefix)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestToolDefinitionEnforcesOutputConstraint(t *testing.T) {
	tool, err := goagent.NewToolFromDefinition(goagent.ToolDefinition{
		Name:        "weather",
		Description: "Get weather.",
		Schema:      goagent.ToolSchema{"type": "object"},
		Function: func(context.Context, string) (string, error) {
			return "too long", nil
		},
		Constraints: goagent.ToolConstraints{MaxOutputBytes: 3},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tool.Call(context.Background(), goagent.ToolCall{Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)})
	if err == nil || !strings.Contains(err.Error(), "max output bytes") {
		t.Fatalf("Call error = %v, want max output bytes error", err)
	}
}

func TestNewToolRejectsUnsupportedFunctionShapes(t *testing.T) {
	tests := []struct {
		name string
		fn   any
	}{
		{name: "nil", fn: nil},
		{name: "not function", fn: "weather"},
		{name: "missing context", fn: func(string) (string, error) { return "", nil }},
		{name: "missing error", fn: func(context.Context, string) string { return "" }},
		{name: "non string input", fn: func(context.Context, int) (string, error) { return "", nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := goagent.NewTool("weather", "Get weather.", tt.fn)
			if err == nil {
				t.Fatal("NewTool succeeded for unsupported function shape")
			}
			if !strings.Contains(err.Error(), "func(context.Context, string|struct) (string, error)") {
				t.Fatalf("error = %q, want supported signature", err)
			}
		})
	}
}

func TestNewToolRejectsInvalidToolNames(t *testing.T) {
	for _, name := range []string{"", "bad name", "bad.name"} {
		t.Run(name, func(t *testing.T) {
			_, err := goagent.NewTool(name, "Get weather.", func(context.Context, string) (string, error) {
				return "", nil
			})
			if err == nil {
				t.Fatal("NewTool succeeded with invalid name")
			}
		})
	}
}

func TestFunctionToolValidatesCallNameAndInput(t *testing.T) {
	tool, err := goagent.NewTool("weather", "Get weather.", func(context.Context, string) (string, error) {
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		call goagent.ToolCall
		want string
	}{
		{
			name: "wrong tool name",
			call: goagent.ToolCall{Name: "forecast", Input: json.RawMessage(`{"city":"Austin"}`)},
			want: "cannot execute call",
		},
		{
			name: "invalid json",
			call: goagent.ToolCall{Name: "weather", Input: json.RawMessage(`not-json`)},
			want: "JSON object",
		},
		{
			name: "empty object",
			call: goagent.ToolCall{Name: "weather", Input: json.RawMessage(`{}`)},
			want: "exactly one string field",
		},
		{
			name: "multiple fields",
			call: goagent.ToolCall{Name: "weather", Input: json.RawMessage(`{"city":"Austin","unit":"F"}`)},
			want: "exactly one string field",
		},
		{
			name: "non string value",
			call: goagent.ToolCall{Name: "weather", Input: json.RawMessage(`{"city":72}`)},
			want: "must be a string",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tool.Call(context.Background(), tt.call)
			if err == nil {
				t.Fatal("Call succeeded with invalid input")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestFunctionToolPreservesReturnedErrors(t *testing.T) {
	wantErr := errors.New("weather service unavailable")
	tool, err := goagent.NewTool("weather", "Get weather.", func(context.Context, string) (string, error) {
		return "", wantErr
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = tool.Call(context.Background(), goagent.ToolCall{
		Name:  "weather",
		Input: json.RawMessage(`{"city":"Austin"}`),
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want wrapped %v", err, wantErr)
	}
}
