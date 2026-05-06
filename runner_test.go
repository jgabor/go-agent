package goagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

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
