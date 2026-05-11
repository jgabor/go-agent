package openai_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	goagent "github.com/jgabor/go-agent"
	"github.com/jgabor/go-agent/providers/openai"
)

func TestChatModelSendsChatCompletionRequest(t *testing.T) {
	var got requestBody
	temperature := 0.25
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Accept") != "text/event-stream" {
			t.Fatalf("Accept = %q", r.Header.Get("Accept"))
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Fatalf("Authorization = %q", r.Header.Get("Authorization"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"id\":\"req-text\",\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Bring a jacket.\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	model := openai.ChatModel{
		Model:      "gpt-test",
		APIKey:     "test-key",
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		Options: openai.ChatOptions{
			ReasoningEffort:    "low",
			ResponseFormat:     openai.ResponseFormat{Type: openai.ResponseFormatJSONObject},
			IncludeStreamUsage: true,
		},
	}
	events, err := streamChatModel(model, goagent.TurnRequest{
		Instructions: "Answer briefly.",
		Messages: []goagent.Message{
			{Role: goagent.RoleUser, Content: "Weather?"},
			{Role: goagent.RoleAssistant, ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}},
			{Role: goagent.RoleTool, Name: "weather", ToolCallID: "call-1", Content: "clear"},
		},
		Tools: []goagent.ToolSpec{{Name: "weather", Description: "Get weather.", Schema: goagent.ToolSchema{"type": "object"}}},
		Options: goagent.TurnOptions{
			MaxOutputTokens: 128,
			Temperature:     &temperature,
			StopSequences:   []string{"END"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := goagent.AssembleStream(events, nil)
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
	if got.MaxCompletionTokens != 128 || got.Temperature == nil || *got.Temperature != temperature || !reflect.DeepEqual(got.Stop, []string{"END"}) {
		t.Fatalf("request options = %+v", got)
	}
	if got.ReasoningEffort != "low" || got.ResponseFormat == nil || got.ResponseFormat.Type != openai.ResponseFormatJSONObject {
		t.Fatalf("provider options = %+v", got.ResponseFormat)
	}
	if !got.Stream || got.StreamOptions == nil || !got.StreamOptions.IncludeUsage {
		t.Fatalf("stream options = %+v", got)
	}
	if result.Text != "Bring a jacket." || result.StopReason != goagent.StopComplete {
		t.Fatalf("assembled result = %+v", result)
	}
	if events[len(events)-1].Kind != goagent.EventStop {
		t.Fatalf("last event = %+v, want stop", events[len(events)-1])
	}
}

func TestChatModelStreamsInterleavedToolCallsUsageAndRawStopWithCanonicalEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("X-Request-Id", "req-interleaved")
		_, _ = w.Write([]byte("data: {\"id\":\"chunk-request\",\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Use tools.\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"call-city\",\"type\":\"function\",\"function\":{\"name\":\"city\",\"arguments\":\"{\\\"q\\\"\"}}]},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-weather\",\"type\":\"function\",\"function\":{\"name\":\"weath\",\"arguments\":\"{\\\"city\\\"\"}}]},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":1,\"function\":{\"arguments\":\":\\\"Austin\\\"}\"}},{\"index\":0,\"function\":{\"name\":\"er\",\"arguments\":\":\\\"Austin\\\"}\"}}]},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":7,\"total_tokens\":18,\"prompt_tokens_details\":{\"cached_tokens\":3},\"completion_tokens_details\":{\"accepted_prediction_tokens\":2}}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	events, err := streamChatModel(openai.ChatModel{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()}, goagent.TurnRequest{})
	if err != nil {
		t.Fatal(err)
	}
	assembled, err := goagent.AssembleEvents(append(events, goagent.Event{Kind: goagent.EventStop, StopReason: goagent.StopComplete}))
	if err != nil {
		t.Fatal(err)
	}
	if assembled.Text != "Use tools." || len(assembled.ToolCalls) != 2 {
		t.Fatalf("assembled = %+v", assembled)
	}
	if assembled.ToolCalls[0].ID != "call-city" || assembled.ToolCalls[0].Name != "city" || string(assembled.ToolCalls[0].Input) != `{"q":"Austin"}` {
		t.Fatalf("first streamed tool call = %+v", assembled.ToolCalls[0])
	}
	if assembled.ToolCalls[1].ID != "call-weather" || assembled.ToolCalls[1].Name != "weather" || string(assembled.ToolCalls[1].Input) != `{"city":"Austin"}` {
		t.Fatalf("second streamed tool call = %+v", assembled.ToolCalls[1])
	}
	if assembled.Usage != (goagent.Usage{InputTokens: 11, OutputTokens: 7, TotalTokens: 18, CachedInputTokens: 3, CacheWriteTokens: 2, RequestID: "req-interleaved", Provider: "openai-compatible", Model: "gpt-test"}) {
		t.Fatalf("usage = %+v", assembled.Usage)
	}
	for _, event := range events {
		assertCanonicalOpenAIEvent(t, event)
		if event.Kind == goagent.EventMessageFinal && event.Diagnostics.RawStopReason != "tool_calls" {
			t.Fatalf("raw stop reason = %q", event.Diagnostics.RawStopReason)
		}
	}
}

func TestChatModelFailureBehaviorFollowsStreamingContract(t *testing.T) {
	for _, model := range []openai.ChatModel{{APIKey: "test-key"}, {Model: "gpt-test"}} {
		var events []goagent.Event
		err := model.Stream(context.Background(), goagent.TurnRequest{}, func(event goagent.Event) {
			events = append(events, event)
		})
		if err == nil {
			t.Fatal("setup failure returned nil error")
		}
		if len(events) != 0 {
			t.Fatalf("setup failure events = %+v, want none", events)
		}
	}

	for _, tt := range []struct {
		name       string
		statusCode int
		contentTyp string
		body       string
		want       string
		wantEvents bool
	}{
		{name: "provider error before accepted event", statusCode: http.StatusBadGateway, contentTyp: "application/json", body: `{"error":{"message":"upstream failed","type":"server_error","code":"bad_gateway"}}`, want: "status 502"},
		{name: "malformed chunk", contentTyp: "text/event-stream", body: "data: {not-json}\n\n", want: "decode SSE chunk", wantEvents: true},
		{name: "accepted stream failure", contentTyp: "text/event-stream", body: "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n", want: "stream ended before finish_reason", wantEvents: true},
		{name: "incomplete tool call", contentTyp: "text/event-stream", body: "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"arguments\":\"{}\"}}]},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n", want: "missing function name", wantEvents: true},
		{name: "invalid JSON arguments", contentTyp: "text/event-stream", body: "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"weather\",\"arguments\":\"{bad\"}}]},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n", want: "not valid JSON", wantEvents: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentTyp)
				w.Header().Set("X-Request-Id", "req-failure")
				if tt.statusCode != 0 {
					w.WriteHeader(tt.statusCode)
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			var events []goagent.Event
			err := (openai.ChatModel{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()}).Stream(context.Background(), sensitiveTurnRequest(), func(event goagent.Event) {
				events = append(events, event)
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
			if tt.wantEvents {
				assertTerminalFailure(t, events, err, goagent.StopModelError)
			} else if len(events) != 0 {
				t.Fatalf("events = %+v, want none before accepted stream", events)
			}
			assertDiagnosticsBounded(t, events)
		})
	}
}

func TestChatModelCancellationStopsAcceptedStreamWithoutContradictoryTerminalEvents(t *testing.T) {
	var serverSawCancel atomic.Bool
	canceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(canceled)
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
		<-r.Context().Done()
		serverSawCancel.Store(true)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []goagent.Event
	err := (openai.ChatModel{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()}).Stream(ctx, goagent.TurnRequest{}, func(event goagent.Event) {
		events = append(events, event)
		if event.Kind == goagent.EventTextDelta {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context canceled", err)
	}
	assertTerminalFailure(t, events, context.Canceled, goagent.StopCanceled)
	for _, event := range events {
		if event.Kind == goagent.EventStop && event.StopReason == goagent.StopComplete {
			t.Fatalf("contradictory complete stop after cancellation: %+v", events)
		}
	}
	if !serverSawCancel.Load() {
		<-canceled
	}
	if !serverSawCancel.Load() {
		t.Fatal("server did not observe request cancellation")
	}
}

func TestChatModelStreamsTextThroughRunnerStopResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"Bring \"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"a jacket.\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	runner, err := goagent.NewRunner(goagent.Agent{Model: openai.ChatModel{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Weather?"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Bring a jacket." || result.StopReason != goagent.StopComplete {
		t.Fatalf("result = %+v", result)
	}
	if !hasEventKind(result.Events, goagent.EventResponseStart) || !hasEventKind(result.Events, goagent.EventStop) {
		t.Fatalf("events missing response_start or stop: %+v", result.Events)
	}
}

func TestChatModelParsesToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"weath\",\"arguments\":\"\"}}]},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"er\",\"arguments\":\"{\\\"city\\\"\"}}]},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\":\\\"Austin\\\"}\"}}]},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	model := openai.ChatModel{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()}
	events, err := streamChatModel(model, goagent.TurnRequest{})
	if err != nil {
		t.Fatal(err)
	}

	var ready goagent.Event
	for _, event := range events {
		if event.Kind == goagent.EventToolCallReady {
			ready = event
		}
	}
	if ready.Kind == "" {
		t.Fatalf("events missing tool_call_ready: %+v", events)
	}
	var deltas []goagent.Event
	for _, event := range events {
		if event.Kind == goagent.EventToolCallDelta {
			deltas = append(deltas, event)
		}
	}
	if len(deltas) != 3 {
		t.Fatalf("tool-call deltas = %+v", deltas)
	}
	for _, delta := range deltas {
		if delta.ToolCallID != "call-1" || delta.ToolCallDelta.Index != 0 {
			t.Fatalf("unstable tool-call delta = %+v", delta)
		}
	}
	call := ready.ToolCall
	if call.ID != "call-1" || call.Name != "weather" || string(call.Input) != `{"city":"Austin"}` {
		t.Fatalf("ToolCall = %+v", call)
	}
	if hasEventKind(events, goagent.EventStop) {
		t.Fatalf("tool-call turn emitted run stop before tool execution: %+v", events)
	}
}

func TestChatModelStreamsUsageWhenPresentAndAllowsAbsence(t *testing.T) {
	for _, tt := range []struct {
		name      string
		usageLine string
		wantUsage goagent.Usage
	}{
		{name: "absent"},
		{
			name:      "present",
			usageLine: "data: {\"model\":\"gpt-test\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5,\"prompt_tokens_details\":{\"cached_tokens\":1}}}\n\n",
			wantUsage: goagent.Usage{InputTokens: 3, OutputTokens: 2, TotalTokens: 5, CachedInputTokens: 1, RequestID: "req-usage", Provider: "openai-compatible", Model: "gpt-test"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("X-Request-Id", "req-usage")
				_, _ = w.Write([]byte("data: {\"model\":\"gpt-test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\n"))
				_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n"))
				if tt.usageLine != "" {
					_, _ = w.Write([]byte(tt.usageLine))
				}
				_, _ = w.Write([]byte("data: [DONE]\n\n"))
			}))
			defer server.Close()

			events, err := streamChatModel(openai.ChatModel{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()}, goagent.TurnRequest{})
			if err != nil {
				t.Fatal(err)
			}
			assembled, err := goagent.AssembleEvents(events)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(assembled.Usage, tt.wantUsage) {
				t.Fatalf("usage = %+v, want %+v", assembled.Usage, tt.wantUsage)
			}
		})
	}
}

func TestChatModelPreservesRawFinishReasonDiagnostics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"length\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	events, err := streamChatModel(openai.ChatModel{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()}, goagent.TurnRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var final goagent.Event
	for _, event := range events {
		if event.Kind == goagent.EventMessageFinal {
			final = event
		}
	}
	if final.Diagnostics.RawStopReason != "length" {
		t.Fatalf("raw stop reason = %q", final.Diagnostics.RawStopReason)
	}
}

func TestChatModelRejectsNonSSEStreamResponseWithoutFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"not streamed"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	var events []goagent.Event
	err := (openai.ChatModel{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()}).Stream(context.Background(), goagent.TurnRequest{}, func(event goagent.Event) {
		events = append(events, event)
	})
	if err == nil || !strings.Contains(err.Error(), "expected SSE") {
		t.Fatalf("error = %v, want expected SSE", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want none", events)
	}
}

func TestChatModelReportsProviderErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req-123")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad request","type":"invalid_request_error","code":"bad_request"}}`))
	}))
	defer server.Close()

	model := openai.ChatModel{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()}
	err := model.Stream(context.Background(), goagent.TurnRequest{}, func(goagent.Event) {})
	if err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("error = %v, want status 400", err)
	}
	var providerErr *goagent.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("error = %T, want ProviderError", err)
	}
	diagnostics := providerErr.Diagnostics
	if diagnostics.Provider != "openai-compatible" || diagnostics.Package != "github.com/jgabor/go-agent/providers/openai" || diagnostics.RequestID != "req-123" || diagnostics.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("diagnostics identity = %+v", diagnostics)
	}
	if diagnostics.ErrorType != "invalid_request_error" || diagnostics.ErrorCode != "bad_request" || diagnostics.Excerpt != "bad request" {
		t.Fatalf("diagnostics error fields = %+v", diagnostics)
	}
}

func TestChatModelRedactsProviderErrorExcerpts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"Authorization: Bearer sk-test api_key=sk-test-url https://user:pass@example.test/path messages: [{role:user,content:secret prompt}] tool_args: {\"city\":\"Austin\"} HOME=/home/test","type":"invalid_request_error","code":"bad_request"}}`))
	}))
	defer server.Close()

	model := openai.ChatModel{Model: "gpt-test", APIKey: "test-key", BaseURL: server.URL, HTTPClient: server.Client()}
	err := model.Stream(context.Background(), goagent.TurnRequest{}, func(goagent.Event) {})
	var providerErr *goagent.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("error = %T, want ProviderError", err)
	}
	excerpt := strings.ToLower(providerErr.Diagnostics.Excerpt)
	for _, forbidden := range []string{"sk-test", "user:pass", "role:user", "secret prompt", `"city":"austin"`, "/home/test"} {
		if strings.Contains(excerpt, forbidden) {
			t.Fatalf("excerpt contains %q: %q", forbidden, providerErr.Diagnostics.Excerpt)
		}
	}
	if !strings.Contains(excerpt, "[redacted]") {
		t.Fatalf("excerpt = %q, want redactions", providerErr.Diagnostics.Excerpt)
	}
}

func TestChatOptionsSurfaceIsTyped(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(openai.ChatOptions{}), reflect.TypeOf(openai.ResponseFormat{})} {
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if field.Type.Kind() == reflect.Map {
				t.Fatalf("%s.%s is an untyped pass-through map", typ.Name(), field.Name)
			}
		}
	}
}

func TestChatModelValidatesConfiguration(t *testing.T) {
	if err := (openai.ChatModel{APIKey: "test-key"}).Stream(context.Background(), goagent.TurnRequest{}, func(goagent.Event) {}); err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("missing model error = %v", err)
	}
	if err := (openai.ChatModel{Model: "gpt-test"}).Stream(context.Background(), goagent.TurnRequest{}, func(goagent.Event) {}); err == nil || !strings.Contains(err.Error(), "API key is required") {
		t.Fatalf("missing API key error = %v", err)
	}
}

func TestChatModelModelCapabilitiesKnownOpenAIModel(t *testing.T) {
	m := openai.ChatModel{Model: "gpt-4o", APIKey: "test-key"}
	caps, ok := goagent.ModelCapabilitiesOf(m)
	if !ok {
		t.Fatal("expected ChatModel to implement ModelCapabilitiesProvider")
	}
	if caps.Provider != "openai-compatible" || caps.ModelID != "gpt-4o" {
		t.Fatalf("capabilities = %+v", caps)
	}
	if caps.MaxContextTokens != 128_000 || caps.MaxOutputTokens != 16_384 {
		t.Fatalf("limits = %+v", caps)
	}
	if !caps.SupportsTools || !caps.SupportsStreaming || caps.SupportsReasoning {
		t.Fatalf("expected gpt-4o tools+stream without advertised reasoning: %+v", caps)
	}
	if caps.AllowedReasoningValues != nil {
		t.Fatalf("AllowedReasoningValues = %#v, want nil", caps.AllowedReasoningValues)
	}

	alias := openai.ChatModel{Model: "gpt-4o-2024-08-06", APIKey: "k"}
	capsAlias, _ := goagent.ModelCapabilitiesOf(alias)
	if capsAlias.MaxContextTokens != caps.MaxContextTokens || capsAlias.MaxOutputTokens != caps.MaxOutputTokens {
		t.Fatalf("alias limits mismatch: %+v vs %+v", capsAlias, caps)
	}
}

func TestChatModelModelCapabilitiesKnownReasoningModel(t *testing.T) {
	m := openai.ChatModel{Model: "o3-mini", APIKey: "test-key"}
	caps, ok := goagent.ModelCapabilitiesOf(m)
	if !ok {
		t.Fatal("expected ChatModel to implement ModelCapabilitiesProvider")
	}
	if caps.MaxContextTokens != 200_000 || caps.MaxOutputTokens != 100_000 {
		t.Fatalf("limits = %+v", caps)
	}
	if !caps.SupportsReasoning {
		t.Fatal("expected SupportsReasoning for o3-mini")
	}
	wantEfforts := []string{"low", "medium", "high"}
	if !slices.Equal(caps.AllowedReasoningValues, wantEfforts) {
		t.Fatalf("AllowedReasoningValues = %#v, want %v", caps.AllowedReasoningValues, wantEfforts)
	}
}

func TestChatModelModelCapabilitiesUnknownModelNoInventedFacts(t *testing.T) {
	m := openai.ChatModel{Model: "gpt-custom-unknown", APIKey: "test-key"}
	caps, ok := goagent.ModelCapabilitiesOf(m)
	if !ok {
		t.Fatal("expected ChatModel to implement ModelCapabilitiesProvider")
	}
	if caps.Provider != "openai-compatible" || caps.ModelID != "gpt-custom-unknown" {
		t.Fatalf("capabilities = %+v", caps)
	}
	if caps.MaxContextTokens != 0 || caps.MaxOutputTokens != 0 {
		t.Fatalf("expected zero limits for unknown model: %+v", caps)
	}
	if caps.SupportsTools || caps.SupportsStreaming || caps.SupportsReasoning {
		t.Fatalf("expected false capability flags for unknown model: %+v", caps)
	}
	if len(caps.AllowedReasoningValues) != 0 {
		t.Fatalf("AllowedReasoningValues = %#v, want empty", caps.AllowedReasoningValues)
	}
}

func streamChatModel(model openai.ChatModel, request goagent.TurnRequest) ([]goagent.Event, error) {
	var events []goagent.Event
	err := model.Stream(context.Background(), request, func(event goagent.Event) {
		events = append(events, event)
	})
	return events, err
}

func hasEventKind(events []goagent.Event, kind goagent.EventKind) bool {
	for _, event := range events {
		if event.Kind == kind {
			return true
		}
	}
	return false
}

func assertTerminalFailure(t *testing.T, events []goagent.Event, streamErr error, wantStop goagent.StopReason) {
	t.Helper()
	assembled, err := goagent.AssembleStream(events, streamErr)
	if !errors.Is(err, streamErr) {
		t.Fatalf("assembled error = %v, want %v from events %+v", err, streamErr, events)
	}
	if !errors.Is(assembled.Err, streamErr) || assembled.StopReason != wantStop {
		t.Fatalf("assembled terminal = %+v, want error %v and stop %q", assembled, streamErr, wantStop)
	}
	if len(events) < 3 || events[0].Kind != goagent.EventResponseStart || events[len(events)-2].Kind != goagent.EventError || events[len(events)-1].Kind != goagent.EventStop {
		t.Fatalf("terminal events = %+v", events)
	}
	if events[len(events)-1].StopReason != wantStop {
		t.Fatalf("stop = %q, want %q", events[len(events)-1].StopReason, wantStop)
	}
}

func assertCanonicalOpenAIEvent(t *testing.T, event goagent.Event) {
	t.Helper()
	switch event.Kind {
	case goagent.EventResponseStart, goagent.EventContentBlockStart, goagent.EventTextDelta, goagent.EventToolCallDelta, goagent.EventContentBlockEnd, goagent.EventMessageFinal, goagent.EventToolCallReady, goagent.EventUsage, goagent.EventStop:
	default:
		t.Fatalf("non-canonical event kind: %+v", event)
	}
	assertDiagnosticsBounded(t, []goagent.Event{event})
}

func assertDiagnosticsBounded(t *testing.T, events []goagent.Event) {
	t.Helper()
	for _, event := range events {
		diagnostics := event.Diagnostics
		values := []string{diagnostics.Provider, diagnostics.Package, diagnostics.RequestID, diagnostics.ErrorType, diagnostics.ErrorCode, diagnostics.RawStopReason, diagnostics.Excerpt}
		for _, value := range values {
			lower := strings.ToLower(value)
			for _, forbidden := range []string{"test-key", "bearer", "answer briefly", "secret prompt", "tool payload", "pricing", "cost", "registry", "config policy", "workdir", "lira", "workflow"} {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("diagnostics leaked %q in %+v", forbidden, diagnostics)
				}
			}
		}
	}
}

func sensitiveTurnRequest() goagent.TurnRequest {
	return goagent.TurnRequest{
		Instructions: "Answer briefly with secret prompt facts.",
		Messages:     []goagent.Message{{Role: goagent.RoleUser, Content: "tool payload should not be diagnostics"}},
		Tools:        []goagent.ToolSpec{{Name: "weather", Description: "tool payload", Schema: goagent.ToolSchema{"type": "object"}}},
	}
}

type requestBody struct {
	Model               string                 `json:"model"`
	Messages            []requestMessage       `json:"messages"`
	Tools               []requestTool          `json:"tools"`
	MaxCompletionTokens int                    `json:"max_completion_tokens"`
	Temperature         *float64               `json:"temperature"`
	Stop                []string               `json:"stop"`
	ReasoningEffort     string                 `json:"reasoning_effort"`
	ResponseFormat      *requestResponseFormat `json:"response_format"`
	Stream              bool                   `json:"stream"`
	StreamOptions       *requestStreamOptions  `json:"stream_options"`
}

type requestStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type requestResponseFormat struct {
	Type openai.ResponseFormatType `json:"type"`
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
