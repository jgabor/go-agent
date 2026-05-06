package goagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	goagent "github.com/jgabor/go-agent"
)

func TestNewRunnerRequiresModelAndValidTools(t *testing.T) {
	if _, err := goagent.NewRunner(goagent.Agent{}); err == nil {
		t.Fatal("NewRunner succeeded without model")
	}

	model := &recordingModel{}
	tool := namedTool{name: "weather"}
	if _, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool, tool}}); err == nil {
		t.Fatal("NewRunner succeeded with duplicate tools")
	}
	if _, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{namedTool{name: "bad name"}}}); err == nil {
		t.Fatal("NewRunner succeeded with invalid tool name")
	}
}

func TestRunnerSendsInputSessionInstructionsAndToolsToModel(t *testing.T) {
	model := &recordingModel{turns: []goagent.TurnResult{{
		Message:    goagent.Message{Role: goagent.RoleAssistant, Content: "Bring a jacket."},
		StopReason: goagent.StopComplete,
	}}}
	tool, err := goagent.NewTool("weather", "Get the weather.", func(context.Context, string) (string, error) {
		return "clear", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := goagent.NewRunner(goagent.Agent{
		Instructions: "Answer with weather advice.",
		Model:        model,
		Tools:        []goagent.Tool{tool},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{
		Input: "Should I bring a jacket?",
		Session: goagent.Session{
			ID:       "session-1",
			Messages: []goagent.Message{{Role: goagent.RoleAssistant, Content: "Earlier answer."}},
			Values:   map[string]any{"tenant": "test"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Bring a jacket." || result.StopReason != goagent.StopComplete {
		t.Fatalf("RunResult = %+v", result)
	}

	request := model.requests[0]
	if request.Instructions != "Answer with weather advice." {
		t.Fatalf("Instructions = %q", request.Instructions)
	}
	if request.Session.ID != "session-1" {
		t.Fatalf("Session.ID = %q", request.Session.ID)
	}
	if len(request.Tools) != 1 || request.Tools[0].Name != "weather" {
		t.Fatalf("Tools = %+v", request.Tools)
	}
	if got := request.Messages[len(request.Messages)-1]; got.Role != goagent.RoleUser || got.Content != "Should I bring a jacket?" {
		t.Fatalf("last model message = %+v", got)
	}
	if got := result.Session.Messages[len(result.Session.Messages)-1]; got.Role != goagent.RoleAssistant || got.Content != "Bring a jacket." {
		t.Fatalf("last result session message = %+v", got)
	}
}

func TestRunnerExposesAdvancedToolDefinitionMetadata(t *testing.T) {
	model := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}},
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "Done."}, StopReason: goagent.StopComplete},
	}}
	tool, err := goagent.NewToolFromDefinition(goagent.ToolDefinition{
		Name:        "weather",
		Description: "Get weather with an explicit schema.",
		Schema: goagent.ToolSchema{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string", "description": "City name."},
			},
			"required": []string{"city"},
		},
		Function: func(context.Context, string) (string, error) {
			return "clear in Austin", nil
		},
		Safety:      goagent.ToolSafety{ReadOnly: true, Retryable: true},
		Constraints: goagent.ToolConstraints{Timeout: time.Second, MaxOutputBytes: 64},
	})
	if err != nil {
		t.Fatal(err)
	}
	var decisions []goagent.Decision
	policy := goagent.PolicyFunc(func(_ context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		decisions = append(decisions, decision)
		return goagent.PolicyDecision{Allowed: true}, nil
	})
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Weather?"})
	if err != nil {
		t.Fatal(err)
	}

	requestTool := model.requests[0].Tools[0]
	assertAdvancedToolSpec(t, requestTool)
	var sawToolCallDecision, sawToolResultDecision bool
	for _, decision := range decisions {
		if decision.Kind == goagent.DecisionToolCall {
			sawToolCallDecision = true
			assertAdvancedToolSpec(t, decision.Tool)
		}
		if decision.Kind == goagent.DecisionToolResult {
			sawToolResultDecision = true
			assertAdvancedToolSpec(t, decision.Tool)
		}
	}
	if !sawToolCallDecision || !sawToolResultDecision {
		t.Fatalf("decisions missing tool metadata: %+v", decisions)
	}
	var sawToolCallEvent, sawToolResultEvent, sawPolicyEvent bool
	for _, event := range result.Events {
		if event.Kind == goagent.EventToolCall {
			sawToolCallEvent = true
			assertAdvancedToolSpec(t, event.Tool)
		}
		if event.Kind == goagent.EventToolResult {
			sawToolResultEvent = true
			assertAdvancedToolSpec(t, event.Tool)
		}
		if event.Kind == goagent.EventPolicyDecision && event.Decision.Kind == goagent.DecisionToolCall {
			sawPolicyEvent = true
			assertAdvancedToolSpec(t, event.Tool)
			assertAdvancedToolSpec(t, event.Decision.Tool)
		}
	}
	if !sawToolCallEvent || !sawToolResultEvent || !sawPolicyEvent {
		t.Fatalf("events missing advanced tool metadata: %+v", result.Events)
	}
}

func TestRunnerUsesRegisteredToolMetadataWithoutRediscovery(t *testing.T) {
	model := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}},
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "Done."}, StopReason: goagent.StopComplete},
	}}
	tool := &metadataCountingTool{
		name: "weather",
		metadata: goagent.ToolMetadata{
			Safety:      goagent.ToolSafety{ReadOnly: true, Retryable: true},
			Constraints: goagent.ToolConstraints{Timeout: time.Second, MaxOutputBytes: 64},
		},
	}
	var decisions []goagent.Decision
	policy := goagent.PolicyFunc(func(_ context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		decisions = append(decisions, decision)
		return goagent.PolicyDecision{Allowed: true}, nil
	})

	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}, Policy: policy, Retry: goagent.RetryPolicy{MaxAttempts: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if tool.metadataCalls != 1 {
		t.Fatalf("Metadata calls after NewRunner = %d, want 1", tool.metadataCalls)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Weather?"})
	if err != nil {
		t.Fatal(err)
	}
	if tool.metadataCalls != 1 {
		t.Fatalf("Metadata calls after Run = %d, want cached construction metadata", tool.metadataCalls)
	}
	assertAdvancedToolSpec(t, model.requests[0].Tools[0])

	var sawPolicyToolCall, sawToolCallEvent, sawToolResultEvent bool
	for _, decision := range decisions {
		if decision.Kind == goagent.DecisionToolCall {
			sawPolicyToolCall = true
			assertAdvancedToolSpec(t, decision.Tool)
		}
	}
	for _, event := range result.Events {
		switch event.Kind {
		case goagent.EventToolCall:
			sawToolCallEvent = true
			assertAdvancedToolSpec(t, event.Tool)
		case goagent.EventToolResult:
			sawToolResultEvent = true
			assertAdvancedToolSpec(t, event.Tool)
		}
	}
	if !sawPolicyToolCall || !sawToolCallEvent || !sawToolResultEvent {
		t.Fatalf("missing cached metadata surfaces: decisions=%+v events=%+v", decisions, result.Events)
	}
}

func TestRunnerClonesCachedToolSchemaAcrossModelPolicyEventsAndRuns(t *testing.T) {
	model := &schemaMutatingModel{}
	tool, err := goagent.NewToolFromDefinition(goagent.ToolDefinition{
		Name:        "weather",
		Description: "Get weather with nested schema metadata.",
		Schema: goagent.ToolSchema{
			"type": "object",
			"properties": map[string]any{
				"city": map[string]any{"type": "string"},
			},
			"required": []string{"city"},
		},
		Function: func(context.Context, string) (string, error) {
			return "clear", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var policyTools []goagent.ToolSpec
	policy := goagent.PolicyFunc(func(_ context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		if decision.Kind == goagent.DecisionToolCall || decision.Kind == goagent.DecisionToolResult {
			policyTools = append(policyTools, decision.Tool)
		}
		return goagent.PolicyDecision{Allowed: true}, nil
	})
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Weather?"})
		if err != nil {
			t.Fatal(err)
		}
		if result.StopReason != goagent.StopComplete {
			t.Fatalf("run %d StopReason = %q, want %q", i+1, result.StopReason, goagent.StopComplete)
		}
		for _, event := range result.Events {
			if event.Kind == goagent.EventToolCall || event.Kind == goagent.EventToolResult {
				assertUnmutatedWeatherSchema(t, event.Tool.Schema)
			}
			if event.Kind == goagent.EventPolicyDecision && (event.Decision.Kind == goagent.DecisionToolCall || event.Decision.Kind == goagent.DecisionToolResult) {
				assertUnmutatedWeatherSchema(t, event.Tool.Schema)
				assertUnmutatedWeatherSchema(t, event.Decision.Tool.Schema)
			}
		}
	}
	if len(policyTools) != 4 {
		t.Fatalf("policy tool surfaces = %d, want 4", len(policyTools))
	}
	for _, spec := range policyTools {
		assertUnmutatedWeatherSchema(t, spec.Schema)
	}
	if model.firstTurns != 2 {
		t.Fatalf("first turns = %d, want one per run", model.firstTurns)
	}
}

func TestRunnerExecutesToolAndFeedsResultBackToModel(t *testing.T) {
	model := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}},
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "Clear in Austin."}, StopReason: goagent.StopComplete},
	}}
	tool, err := goagent.NewTool("weather", "Get the weather.", func(ctx context.Context, city string) (string, error) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "clear in " + city, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Weather in Austin?"})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != goagent.StopComplete || result.Text != "Clear in Austin." {
		t.Fatalf("RunResult = %+v", result)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model turns = %d, want 2", len(model.requests))
	}
	secondMessages := model.requests[1].Messages
	if got := secondMessages[len(secondMessages)-1]; got.Role != goagent.RoleTool || got.ToolCallID != "call-1" || got.Content != "clear in Austin" {
		t.Fatalf("tool result was not fed back to model: %+v", got)
	}
	assertEventKinds(t, result.Events, []goagent.EventKind{
		goagent.EventToolCall,
		goagent.EventPolicyDecision,
		goagent.EventToolResult,
		goagent.EventTextDelta,
		goagent.EventStop,
	})
	assertOrderedCorrelatedEvents(t, result.Events)
}

func TestRunnerStopsOnErrorsPolicyStepLimitAndCancellation(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		request  goagent.RunRequest
		agent    goagent.Agent
		wantStop goagent.StopReason
	}{
		{
			name:     "model error",
			ctx:      context.Background(),
			agent:    goagent.Agent{Model: &recordingModel{err: errors.New("model failed")}},
			wantStop: goagent.StopModelError,
		},
		{
			name: "unknown tool",
			ctx:  context.Background(),
			agent: goagent.Agent{Model: &recordingModel{turns: []goagent.TurnResult{{
				ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "missing", Input: json.RawMessage(`{}`)}},
			}}}},
			wantStop: goagent.StopToolError,
		},
		{
			name: "tool error",
			ctx:  context.Background(),
			agent: goagent.Agent{
				Model: &recordingModel{turns: []goagent.TurnResult{{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}}}},
				Tools: []goagent.Tool{namedTool{name: "weather", err: errors.New("tool failed")}},
			},
			wantStop: goagent.StopToolError,
		},
		{
			name: "policy denial",
			ctx:  context.Background(),
			agent: goagent.Agent{
				Model:  &recordingModel{turns: []goagent.TurnResult{{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{}`)}}}}},
				Tools:  []goagent.Tool{namedTool{name: "weather"}},
				Policy: denyAllPolicy,
			},
			wantStop: goagent.StopPolicyDenied,
		},
		{
			name:    "step limit",
			ctx:     context.Background(),
			request: goagent.RunRequest{MaxSteps: 1},
			agent: goagent.Agent{
				Model: &recordingModel{turns: []goagent.TurnResult{
					{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{}`)}}},
					{Message: goagent.Message{Content: "unreached"}},
				}},
				Tools: []goagent.Tool{namedTool{name: "weather"}},
			},
			wantStop: goagent.StopStepLimit,
		},
		{
			name:     "cancellation",
			ctx:      canceledContext(),
			agent:    goagent.Agent{Model: &recordingModel{turns: []goagent.TurnResult{{Message: goagent.Message{Content: "unreached"}}}}},
			wantStop: goagent.StopCanceled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner, err := goagent.NewRunner(tt.agent)
			if err != nil {
				t.Fatal(err)
			}
			result, err := runner.Run(tt.ctx, tt.request)
			if err != nil {
				t.Fatal(err)
			}
			if result.StopReason != tt.wantStop {
				t.Fatalf("StopReason = %q, want %q", result.StopReason, tt.wantStop)
			}
			if len(result.Events) == 0 || result.Events[len(result.Events)-1].Kind != goagent.EventStop {
				t.Fatalf("events do not end with stop: %+v", result.Events)
			}
		})
	}
}

func TestRunnerPolicyCanConstrainToolCallInput(t *testing.T) {
	model := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}},
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "Done."}, StopReason: goagent.StopComplete},
	}}
	tool, err := goagent.NewTool("weather", "Get weather.", func(ctx context.Context, city string) (string, error) {
		return "clear in " + city, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	policy := goagent.PolicyFunc(func(ctx context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		if decision.Kind != goagent.DecisionToolCall {
			return goagent.PolicyDecision{Allowed: true}, nil
		}
		call := decision.ToolCall
		call.Input = json.RawMessage(`{"city":"Berlin"}`)
		return goagent.PolicyDecision{Allowed: true, Reason: "rewrite city", ToolCall: &call}, nil
	})
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Weather?"})
	if err != nil {
		t.Fatal(err)
	}

	if got := model.requests[1].Messages[len(model.requests[1].Messages)-1]; got.Content != "clear in Berlin" {
		t.Fatalf("tool result message = %+v", got)
	}
	var found bool
	for _, event := range result.Events {
		if event.Kind == goagent.EventPolicyDecision && event.Decision.Kind == goagent.DecisionToolCall {
			found = true
			if event.PolicyDecision.ToolCall == nil || string(event.PolicyDecision.ToolCall.Input) != `{"city":"Berlin"}` {
				t.Fatalf("policy event = %+v", event)
			}
		}
	}
	if !found {
		t.Fatal("missing tool-call policy decision event")
	}
}

func TestRunnerPolicyCanConstrainStepLimit(t *testing.T) {
	model := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}},
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "unreached"}},
	}}
	tool := namedTool{name: "weather"}
	policy := goagent.PolicyFunc(func(ctx context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		if decision.Kind == goagent.DecisionRunStart {
			return goagent.PolicyDecision{Allowed: true, Reason: "budget", MaxSteps: 1}, nil
		}
		return goagent.PolicyDecision{Allowed: true}, nil
	})
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Weather?", MaxSteps: 10})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != goagent.StopStepLimit {
		t.Fatalf("StopReason = %q, want %q", result.StopReason, goagent.StopStepLimit)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model turns = %d, want 1", len(model.requests))
	}
}

func TestRunnerPolicyCanValidateToolResult(t *testing.T) {
	model := &recordingModel{turns: []goagent.TurnResult{{
		ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}},
	}}}
	tool := namedTool{name: "weather"}
	policy := goagent.PolicyFunc(func(ctx context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		if decision.Kind == goagent.DecisionToolResult {
			return goagent.PolicyDecision{Allowed: false, Reason: "blocked output"}, nil
		}
		return goagent.PolicyDecision{Allowed: true}, nil
	})
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Weather?"})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != goagent.StopPolicyDenied {
		t.Fatalf("StopReason = %q, want %q", result.StopReason, goagent.StopPolicyDenied)
	}
	for _, event := range result.Events {
		if event.Kind == goagent.EventToolResult {
			t.Fatalf("tool result event emitted after result denial: %+v", result.Events)
		}
	}
}

func TestRunnerStreamEmitsRunEvents(t *testing.T) {
	model := &recordingModel{turns: []goagent.TurnResult{{Message: goagent.Message{Content: "Done."}}}}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model})
	if err != nil {
		t.Fatal(err)
	}

	events, err := runner.Stream(context.Background(), goagent.RunRequest{Input: "Run"})
	if err != nil {
		t.Fatal(err)
	}

	var kinds []goagent.EventKind
	for event := range events {
		kinds = append(kinds, event.Kind)
	}
	if !slices.Equal(kinds, []goagent.EventKind{goagent.EventTextDelta, goagent.EventStop}) {
		t.Fatalf("stream event kinds = %v", kinds)
	}
}

func assertAdvancedToolSpec(t *testing.T, spec goagent.ToolSpec) {
	t.Helper()
	if spec.Name != "weather" || spec.Description == "" || spec.Schema["type"] != "object" {
		t.Fatalf("ToolSpec model metadata = %+v", spec)
	}
	if !spec.Safety.ReadOnly || !spec.Safety.Retryable {
		t.Fatalf("ToolSpec safety metadata = %+v", spec.Safety)
	}
	if spec.Constraints.Timeout != time.Second || spec.Constraints.MaxOutputBytes != 64 {
		t.Fatalf("ToolSpec constraints = %+v", spec.Constraints)
	}
}

func assertUnmutatedWeatherSchema(t *testing.T, schema goagent.ToolSchema) {
	t.Helper()
	if err := unmutatedWeatherSchemaError(schema); err != nil {
		t.Fatalf("%v", err)
	}
}

func unmutatedWeatherSchemaError(schema goagent.ToolSchema) error {
	if schema["type"] != "object" {
		return fmt.Errorf("schema type = %v, want object: %+v", schema["type"], schema)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return fmt.Errorf("schema properties = %T, want map[string]any", schema["properties"])
	}
	city, ok := properties["city"].(map[string]any)
	if !ok {
		return fmt.Errorf("city schema = %T, want map[string]any", properties["city"])
	}
	if city["type"] != "string" {
		return fmt.Errorf("city schema type = %v, want string: %+v", city["type"], schema)
	}
	return nil
}

type recordingModel struct {
	turns    []goagent.TurnResult
	err      error
	next     int
	requests []goagent.TurnRequest
}

func (m *recordingModel) Turn(_ context.Context, request goagent.TurnRequest) (goagent.TurnResult, error) {
	m.requests = append(m.requests, request)
	if m.err != nil {
		return goagent.TurnResult{}, m.err
	}
	if m.next >= len(m.turns) {
		return goagent.TurnResult{}, errors.New("unexpected model turn")
	}
	turn := m.turns[m.next]
	m.next++
	return turn, nil
}

type namedTool struct {
	name string
	err  error
}

func (t namedTool) Name() string { return t.name }

func (namedTool) Description() string { return "test tool" }

func (namedTool) Schema() goagent.ToolSchema { return goagent.ToolSchema{"type": "object"} }

func (t namedTool) Call(context.Context, goagent.ToolCall) (goagent.ToolResult, error) {
	if t.err != nil {
		return goagent.ToolResult{}, t.err
	}
	return goagent.ToolResult{CallID: "call-1", Name: t.name, Content: "tool result"}, nil
}

type metadataCountingTool struct {
	name          string
	metadata      goagent.ToolMetadata
	metadataCalls int
}

func (t *metadataCountingTool) Name() string { return t.name }

func (*metadataCountingTool) Description() string { return "Get weather with cached metadata." }

func (*metadataCountingTool) Schema() goagent.ToolSchema { return goagent.ToolSchema{"type": "object"} }

func (t *metadataCountingTool) Metadata() goagent.ToolMetadata {
	t.metadataCalls++
	return t.metadata
}

func (t *metadataCountingTool) Call(context.Context, goagent.ToolCall) (goagent.ToolResult, error) {
	return goagent.ToolResult{CallID: "call-1", Name: t.name, Content: "tool result"}, nil
}

type schemaMutatingModel struct {
	firstTurns int
}

func (m *schemaMutatingModel) Turn(_ context.Context, request goagent.TurnRequest) (goagent.TurnResult, error) {
	if len(request.Messages) > 0 && request.Messages[len(request.Messages)-1].Role == goagent.RoleTool {
		return goagent.TurnResult{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "Done."}, StopReason: goagent.StopComplete}, nil
	}
	if len(request.Tools) != 1 {
		return goagent.TurnResult{}, fmt.Errorf("tools = %d, want 1", len(request.Tools))
	}
	if err := unmutatedWeatherSchemaError(request.Tools[0].Schema); err != nil {
		return goagent.TurnResult{}, err
	}
	m.firstTurns++
	request.Tools[0].Schema["type"] = "mutated"
	properties := request.Tools[0].Schema["properties"].(map[string]any)
	city := properties["city"].(map[string]any)
	city["type"] = "number"
	return goagent.TurnResult{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}}, nil
}
