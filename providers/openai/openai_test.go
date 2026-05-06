package openai_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	goagent "github.com/jgabor/go-agent"
	"github.com/jgabor/go-agent/providers/openai"
)

func TestChatModelSendsChatCompletionRequest(t *testing.T) {
	var got requestBody
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Bring a jacket."},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	model := openai.ChatModel{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()}
	result, err := model.Turn(context.Background(), goagent.TurnRequest{
		Instructions: "Answer briefly.",
		Messages: []goagent.Message{
			{Role: goagent.RoleUser, Content: "Weather?"},
			{Role: goagent.RoleAssistant, ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}},
			{Role: goagent.RoleTool, Name: "weather", ToolCallID: "call-1", Content: "clear"},
		},
		Tools: []goagent.ToolSpec{{Name: "weather", Description: "Get weather.", Schema: goagent.ToolSchema{"type": "object"}}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if got.Model != "gpt-test" {
		t.Fatalf("model = %q", got.Model)
	}
	if len(got.Messages) != 4 {
		t.Fatalf("messages = %+v", got.Messages)
	}
	if got.Messages[0].Role != "system" || got.Messages[0].Content != "Answer briefly." {
		t.Fatalf("system message = %+v", got.Messages[0])
	}
	if got.Messages[2].Role != "assistant" || len(got.Messages[2].ToolCalls) != 1 || got.Messages[2].ToolCalls[0].Function.Name != "weather" {
		t.Fatalf("assistant tool-call message = %+v", got.Messages[2])
	}
	if got.Messages[3].Role != "tool" || got.Messages[3].ToolCallID != "call-1" {
		t.Fatalf("tool message = %+v", got.Messages[3])
	}
	if len(got.Tools) != 1 || got.Tools[0].Type != "function" || got.Tools[0].Function.Name != "weather" {
		t.Fatalf("tools = %+v", got.Tools)
	}
	if result.Message.Content != "Bring a jacket." || result.StopReason != goagent.StopComplete {
		t.Fatalf("TurnResult = %+v", result)
	}
}

func TestChatModelParsesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"call-1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Austin\"}"}}]},"finish_reason":"tool_calls"}]}`))
	}))
	defer server.Close()

	model := openai.ChatModel{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()}
	result, err := model.Turn(context.Background(), goagent.TurnRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if result.StopReason != "" {
		t.Fatalf("StopReason = %q, want empty while tool calls are pending", result.StopReason)
	}
	if len(result.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v", result.ToolCalls)
	}
	if len(result.Message.ToolCalls) != 1 {
		t.Fatalf("Message.ToolCalls = %+v", result.Message.ToolCalls)
	}
	call := result.ToolCalls[0]
	if call.ID != "call-1" || call.Name != "weather" || string(call.Input) != `{"city":"Austin"}` {
		t.Fatalf("ToolCall = %+v", call)
	}
}

func TestChatModelReportsProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	model := openai.ChatModel{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()}
	_, err := model.Turn(context.Background(), goagent.TurnRequest{})
	if err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("error = %v, want status 400", err)
	}
}

func TestChatModelValidatesConfiguration(t *testing.T) {
	if _, err := (openai.ChatModel{APIKey: "test-key"}).Turn(context.Background(), goagent.TurnRequest{}); err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("missing model error = %v", err)
	}
	if _, err := (openai.ChatModel{Model: "gpt-test"}).Turn(context.Background(), goagent.TurnRequest{}); err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Fatalf("missing API key error = %v", err)
	}
}

type requestBody struct {
	Model    string           `json:"model"`
	Messages []requestMessage `json:"messages"`
	Tools    []requestTool    `json:"tools"`
}

type requestMessage struct {
	Role       string            `json:"role"`
	Content    string            `json:"content"`
	Name       string            `json:"name"`
	ToolCallID string            `json:"tool_call_id"`
	ToolCalls  []requestToolCall `json:"tool_calls"`
}

type requestTool struct {
	Type     string          `json:"type"`
	Function requestFunction `json:"function"`
}

type requestFunction struct {
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Parameters  goagent.ToolSchema `json:"parameters"`
}

type requestToolCall struct {
	ID       string                  `json:"id"`
	Type     string                  `json:"type"`
	Function requestToolCallFunction `json:"function"`
}

type requestToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
