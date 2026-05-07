package goagent_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	goagent "github.com/jgabor/go-agent"
)

func TestNewValidatesBeforeRun(t *testing.T) {
	if _, err := goagent.New(); err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("New() error = %v, want useful missing model error", err)
	}

	if _, err := goagent.New(nil); err == nil || !strings.Contains(err.Error(), "option") {
		t.Fatalf("New(nil) error = %v, want useful option error", err)
	}

	if _, err := goagent.New(goagent.WithModel(nil)); err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("New(WithModel(nil)) error = %v, want useful model error", err)
	}

	if _, err := goagent.New(goagent.WithModel(&recordingModel{}), goagent.WithPolicy(nil)); err == nil || !strings.Contains(err.Error(), "policy") {
		t.Fatalf("New(WithPolicy(nil)) error = %v, want useful policy error", err)
	}

	if _, err := goagent.New(goagent.WithModel(&recordingModel{}), goagent.WithSessionStore(nil)); err == nil || !strings.Contains(err.Error(), "session store") {
		t.Fatalf("New(WithSessionStore(nil)) error = %v, want useful session store error", err)
	}

	if _, err := goagent.New(goagent.WithModel(&recordingModel{}), goagent.WithTools(nil)); err == nil || !strings.Contains(err.Error(), "tool") {
		t.Fatalf("New(WithTools(nil)) error = %v, want useful tool error", err)
	}

	if _, err := goagent.New(goagent.WithModel(&recordingModel{}), goagent.WithEventSinks(nil)); err == nil || !strings.Contains(err.Error(), "event sink") {
		t.Fatalf("New(WithEventSinks(nil)) error = %v, want useful event sink error", err)
	}

	if _, err := goagent.New(goagent.WithModel(&recordingModel{}), goagent.WithRetry(goagent.RetryPolicy{MaxAttempts: -1})); err == nil || !strings.Contains(err.Error(), "retry") {
		t.Fatalf("New(WithRetry(negative attempts)) error = %v, want useful retry error", err)
	}

	if _, err := goagent.New(goagent.WithModel(&recordingModel{}), goagent.WithRetry(goagent.RetryPolicy{Delay: -time.Nanosecond})); err == nil || !strings.Contains(err.Error(), "retry") {
		t.Fatalf("New(WithRetry(negative delay)) error = %v, want useful retry error", err)
	}
}

func TestNewAppliesSafeRuntimeDefaults(t *testing.T) {
	model := &recordingModel{turns: []goagent.TurnResult{{
		Message:    goagent.Message{Role: goagent.RoleAssistant, Content: "Done."},
		StopReason: goagent.StopComplete,
	}}}
	runner, err := goagent.New(goagent.WithModel(model))
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Run"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "Done." || result.StopReason != goagent.StopComplete {
		t.Fatalf("RunResult = %+v", result)
	}
	if len(model.requests) != 1 {
		t.Fatalf("model requests = %d, want 1", len(model.requests))
	}
	request := model.requests[0]
	if request.Instructions != "" {
		t.Fatalf("default instructions = %q, want empty", request.Instructions)
	}
	if len(request.Tools) != 0 {
		t.Fatalf("default tools = %+v, want none", request.Tools)
	}
	if !slices.Equal(eventKinds(result.Events), textTurnEvents()) {
		t.Fatalf("default events = %+v", eventKinds(result.Events))
	}
}

func TestNewIsEquivalentToAgentNewRunner(t *testing.T) {
	turn := goagent.TurnResult{
		Message:    goagent.Message{Role: goagent.RoleAssistant, Content: "Bring a jacket."},
		StopReason: goagent.StopComplete,
	}
	tool := namedTool{name: "weather"}
	policy := goagent.PolicyFunc(func(context.Context, goagent.Decision) (goagent.PolicyDecision, error) {
		return goagent.PolicyDecision{Allowed: true}, nil
	})
	facadeStore := goagent.NewMemorySessionStore()
	explicitStore := goagent.NewMemorySessionStore()
	var facadeEvents []goagent.Event
	var explicitEvents []goagent.Event

	facadeModel := &recordingModel{turns: []goagent.TurnResult{turn}}
	facadeRunner, err := goagent.New(
		goagent.WithInstructions("Answer with weather advice."),
		goagent.WithModel(facadeModel),
		goagent.WithTools(tool),
		goagent.WithPolicy(policy),
		goagent.WithSessionStore(facadeStore),
		goagent.WithEventSinks(goagent.EventSinkFunc(func(ctx context.Context, event goagent.Event) {
			facadeEvents = append(facadeEvents, event)
		})),
	)
	if err != nil {
		t.Fatal(err)
	}

	explicitModel := &recordingModel{turns: []goagent.TurnResult{turn}}
	explicitRunner, err := goagent.NewRunner(goagent.Agent{
		Instructions: "Answer with weather advice.",
		Model:        explicitModel,
		Tools:        []goagent.Tool{tool},
		Policy:       policy,
		SessionStore: explicitStore,
		EventSinks: []goagent.EventSink{goagent.EventSinkFunc(func(ctx context.Context, event goagent.Event) {
			explicitEvents = append(explicitEvents, event)
		})},
	})
	if err != nil {
		t.Fatal(err)
	}

	request := goagent.RunRequest{Input: "Should I bring a jacket?", SessionID: "session-1"}
	facadeResult, err := facadeRunner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	explicitResult, err := explicitRunner.Run(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}

	if facadeResult.Text != explicitResult.Text || facadeResult.StopReason != explicitResult.StopReason {
		t.Fatalf("facade result = %+v, explicit result = %+v", facadeResult, explicitResult)
	}
	if facadeModel.requests[0].Instructions != explicitModel.requests[0].Instructions {
		t.Fatalf("facade instructions = %q, explicit instructions = %q", facadeModel.requests[0].Instructions, explicitModel.requests[0].Instructions)
	}
	if len(facadeModel.requests[0].Tools) != len(explicitModel.requests[0].Tools) {
		t.Fatalf("facade tools = %+v, explicit tools = %+v", facadeModel.requests[0].Tools, explicitModel.requests[0].Tools)
	}
	if !slices.Equal(eventKinds(facadeEvents), eventKinds(explicitEvents)) {
		t.Fatalf("facade sink events = %v, explicit sink events = %v", eventKinds(facadeEvents), eventKinds(explicitEvents))
	}
	facadeSession, err := facadeStore.LoadSession(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	explicitSession, err := explicitStore.LoadSession(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(facadeSession.Messages) != len(explicitSession.Messages) {
		t.Fatalf("facade stored session = %+v, explicit stored session = %+v", facadeSession, explicitSession)
	}
}
