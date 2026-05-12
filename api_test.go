package goagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	goagent "github.com/jgabor/go-agent"
)

var (
	_ goagent.Model  = fakeModel{}
	_ goagent.Tool   = fakeTool{}
	_ goagent.Policy = fakePolicy{}
	_ goagent.Runner = fakeRunner{}
)

func TestModelStreamContractCarriesMessagesToolsAndSession(t *testing.T) {
	temperature := 0.2
	request := goagent.TurnRequest{
		Instructions: "Answer with weather advice.",
		Messages: []goagent.Message{
			{Role: goagent.RoleUser, Content: "Should I bring a jacket?"},
		},
		Tools: []goagent.ToolSpec{
			{
				Name:        "weather",
				Description: "Get weather for a city.",
				Schema: goagent.ToolSchema{
					"type": "object",
					"properties": map[string]any{
						"city": map[string]any{"type": "string"},
					},
					"required": []string{"city"},
				},
			},
		},
		Session: goagent.Session{ID: "session-1"},
		Options: goagent.TurnOptions{MaxOutputTokens: 128, Temperature: &temperature, StopSequences: []string{"END"}},
	}

	var events []goagent.Event
	err := fakeModel{}.Stream(context.Background(), request, func(event goagent.Event) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	assembled, err := goagent.AssembleStream(append(events, goagent.Event{Kind: goagent.EventStop, StopReason: goagent.StopComplete}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if assembled.StopReason != goagent.StopComplete {
		t.Fatalf("StopReason = %q, want %q", assembled.StopReason, goagent.StopComplete)
	}
	if assembled.Messages[0].Role != goagent.RoleAssistant {
		t.Fatalf("Message.Role = %q, want %q", assembled.Messages[0].Role, goagent.RoleAssistant)
	}
}

func TestRunnerPassesProviderNeutralTurnOptions(t *testing.T) {
	temperature := 0.7
	var got goagent.TurnOptions
	runner, err := goagent.NewRunner(goagent.Agent{
		Model: goagent.ModelFromSimple(goagent.SimpleModelFunc(func(_ context.Context, request goagent.TurnRequest) (goagent.TurnResult, error) {
			got = request.Options
			return goagent.TurnResult{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "ok"}}, nil
		})),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = runner.Run(context.Background(), goagent.RunRequest{
		Input:   "go",
		Options: goagent.TurnOptions{MaxOutputTokens: 64, Temperature: &temperature, StopSequences: []string{"STOP", "END"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxOutputTokens != 64 || got.Temperature == nil || *got.Temperature != temperature || !reflect.DeepEqual(got.StopSequences, []string{"STOP", "END"}) {
		t.Fatalf("TurnOptions = %+v", got)
	}
}

func TestProviderDiagnosticsSurfaceIsBounded(t *testing.T) {
	diagnosticsType := reflect.TypeOf(goagent.ProviderDiagnostics{})
	got := map[string]bool{}
	for i := 0; i < diagnosticsType.NumField(); i++ {
		field := diagnosticsType.Field(i)
		got[field.Name] = true
		if field.Type.Kind() == reflect.Map {
			t.Fatalf("ProviderDiagnostics field %q is an unbounded map", field.Name)
		}
	}
	for _, want := range []string{"Provider", "Package", "RequestID", "HTTPStatus", "ErrorType", "ErrorCode", "RawStopReason", "Excerpt"} {
		if !got[want] {
			t.Fatalf("ProviderDiagnostics missing %q", want)
		}
	}
	for _, forbidden := range []string{"api", "key", "auth", "header", "prompt", "message", "argument", "environment", "url", "price", "registry", "workflow", "policy", "workdir", "setting"} {
		for field := range got {
			if strings.Contains(strings.ToLower(field), forbidden) {
				t.Fatalf("ProviderDiagnostics field %q can represent forbidden concern %q", field, forbidden)
			}
		}
	}
}

func TestUsageSurfaceIsClosedAndTyped(t *testing.T) {
	usageType := reflect.TypeOf(goagent.Usage{})
	got := map[string]bool{}
	for i := 0; i < usageType.NumField(); i++ {
		field := usageType.Field(i)
		got[field.Name] = true
		if field.Type.Kind() == reflect.Map || field.Type.Kind() == reflect.Interface {
			t.Fatalf("Usage field %q is an arbitrary extension surface", field.Name)
		}
	}
	for _, want := range []string{"InputTokens", "OutputTokens", "TotalTokens", "CachedInputTokens", "CacheWriteTokens", "ReasoningTokens", "RequestID", "Provider", "Model"} {
		if !got[want] {
			t.Fatalf("Usage missing %q", want)
		}
	}
	for _, forbidden := range []string{"meta", "cost", "price", "currency", "budget", "marketplace", "registry", "policy"} {
		for field := range got {
			if strings.Contains(strings.ToLower(field), forbidden) {
				t.Fatalf("Usage field %q can represent forbidden concern %q", field, forbidden)
			}
		}
	}
}

func TestProviderErrorCarriesDiagnostics(t *testing.T) {
	cause := errors.New("transport")
	err := &goagent.ProviderError{
		Message: "goagent/openai: status 429",
		Diagnostics: goagent.ProviderDiagnostics{
			Provider:   "openai-compatible",
			Package:    "github.com/jgabor/go-agent/providers/openai",
			RequestID:  "req-1",
			HTTPStatus: 429,
			ErrorType:  "rate_limit_error",
			ErrorCode:  "rate_limit_exceeded",
			Excerpt:    "rate limited",
		},
		Err: cause,
	}
	if !errors.Is(err, cause) {
		t.Fatalf("ProviderError did not unwrap cause")
	}
	if err.Diagnostics.HTTPStatus != 429 || err.Diagnostics.RequestID != "req-1" {
		t.Fatalf("Diagnostics = %+v", err.Diagnostics)
	}
}

func TestPolicyFuncAdaptsHostDecisionLogic(t *testing.T) {
	policy := goagent.PolicyFunc(func(_ context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		return goagent.PolicyDecision{
			Allowed: decision.ToolCall.Name == "weather",
			Reason:  "weather is allowed in this test",
		}, nil
	})

	decision, err := policy.Decide(context.Background(), goagent.Decision{
		ToolCall: goagent.ToolCall{Name: "weather"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !decision.Allowed {
		t.Fatal("PolicyFunc denied the expected tool call")
	}
}

func TestToolDefinitionSurfaceStaysRuntimeOnly(t *testing.T) {
	typ := reflect.TypeOf(goagent.ToolDefinition{})
	for i := 0; i < typ.NumField(); i++ {
		field := strings.ToLower(typ.Field(i).Name)
		for _, forbidden := range []string{"ui", "render", "view", "market", "package", "auth", "setting", "mcp", "prompt", "resource", "workflow", "agent"} {
			if strings.Contains(field, forbidden) {
				t.Fatalf("ToolDefinition field %q leaks non-runtime concern %q", typ.Field(i).Name, forbidden)
			}
		}
	}
}

type fakeModel struct{}

func (fakeModel) Turn(_ context.Context, request goagent.TurnRequest) (goagent.TurnResult, error) {
	if len(request.Tools) == 0 {
		return goagent.TurnResult{}, nil
	}
	return goagent.TurnResult{
		Message:    goagent.Message{Role: goagent.RoleAssistant, Content: "Bring a jacket."},
		StopReason: goagent.StopComplete,
	}, nil
}

func (m fakeModel) Stream(ctx context.Context, request goagent.TurnRequest, emit func(goagent.Event)) error {
	turn, err := m.Turn(ctx, request)
	if err != nil {
		return err
	}
	goagent.StreamTurnResult(turn, emit)
	return nil
}

type fakeTool struct{}

func (fakeTool) Name() string { return "weather" }

func (fakeTool) Description() string { return "Get weather for a city." }

func (fakeTool) Schema() goagent.ToolSchema {
	return goagent.ToolSchema{"type": "object"}
}

func (fakeTool) Call(_ context.Context, call goagent.ToolCall) (goagent.ToolResult, error) {
	var input struct {
		City string `json:"city"`
	}
	if err := json.Unmarshal(call.Input, &input); err != nil {
		return goagent.ToolResult{}, err
	}
	return goagent.ToolResult{CallID: call.ID, Name: call.Name, Content: "clear in " + input.City}, nil
}

type fakePolicy struct{}

func (fakePolicy) Decide(context.Context, goagent.Decision) (goagent.PolicyDecision, error) {
	return goagent.PolicyDecision{Allowed: true}, nil
}

type fakeRunner struct{}

func (fakeRunner) Run(context.Context, goagent.RunRequest) (goagent.RunResult, error) {
	return goagent.RunResult{Text: "Bring a jacket.", StopReason: goagent.StopComplete}, nil
}

func (fakeRunner) Stream(context.Context, goagent.RunRequest) (<-chan goagent.Event, error) {
	events := make(chan goagent.Event, 1)
	events <- goagent.Event{Kind: goagent.EventStop, StopReason: goagent.StopComplete}
	close(events)
	return events, nil
}
