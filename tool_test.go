package goagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

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
			if !strings.Contains(err.Error(), "func(context.Context, string) (string, error)") {
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
