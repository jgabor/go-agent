package goagent_test

import (
	"context"
	"encoding/json"
	"testing"

	goagent "github.com/jgabor/go-agent"
)

var (
	_ goagent.Model  = fakeModel{}
	_ goagent.Tool   = fakeTool{}
	_ goagent.Policy = fakePolicy{}
	_ goagent.Runner = fakeRunner{}
)

func TestModelTurnContractCarriesMessagesToolsAndSession(t *testing.T) {
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
	}

	result, err := fakeModel{}.Turn(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != goagent.StopComplete {
		t.Fatalf("StopReason = %q, want %q", result.StopReason, goagent.StopComplete)
	}
	if result.Message.Role != goagent.RoleAssistant {
		t.Fatalf("Message.Role = %q, want %q", result.Message.Role, goagent.RoleAssistant)
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
