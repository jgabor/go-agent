package goagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	goagent "github.com/jgabor/go-agent"
)

func TestMemorySessionStoreSavesAndLoadsCopies(t *testing.T) {
	store := goagent.NewMemorySessionStore()
	session := goagent.Session{
		ID:       "session-1",
		Messages: []goagent.Message{{Role: goagent.RoleUser, Content: "hello"}},
		Values:   map[string]any{"tenant": "test"},
	}
	if err := store.SaveSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}

	session.Messages[0].Content = "changed"
	session.Values["tenant"] = "changed"

	loaded, err := store.LoadSession(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Messages[0].Content != "hello" || loaded.Values["tenant"] != "test" {
		t.Fatalf("LoadSession returned mutated state: %+v", loaded)
	}

	loaded.Messages[0].Content = "changed again"
	reloaded, err := store.LoadSession(context.Background(), "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Messages[0].Content != "hello" {
		t.Fatalf("LoadSession did not return a copy: %+v", reloaded)
	}
}

func TestRunnerPersistsAndResumesSessionByID(t *testing.T) {
	store := goagent.NewMemorySessionStore()
	model := &recordingModel{turns: []goagent.TurnResult{
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "First answer."}, StopReason: goagent.StopComplete},
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "Second answer."}, StopReason: goagent.StopComplete},
	}}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, SessionStore: store})
	if err != nil {
		t.Fatal(err)
	}

	first, err := runner.Run(context.Background(), goagent.RunRequest{SessionID: "conversation-1", Input: "first"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Session.ID != "conversation-1" {
		t.Fatalf("first Session.ID = %q", first.Session.ID)
	}

	second, err := runner.Run(context.Background(), goagent.RunRequest{SessionID: "conversation-1", Input: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Session.ID != "conversation-1" {
		t.Fatalf("second Session.ID = %q", second.Session.ID)
	}

	secondRequest := model.requests[1]
	if len(secondRequest.Messages) != 3 {
		t.Fatalf("second request messages = %+v", secondRequest.Messages)
	}
	if got := secondRequest.Messages[0]; got.Role != goagent.RoleUser || got.Content != "first" {
		t.Fatalf("first persisted message = %+v", got)
	}
	if got := secondRequest.Messages[1]; got.Role != goagent.RoleAssistant || got.Content != "First answer." {
		t.Fatalf("assistant persisted message = %+v", got)
	}
	if got := secondRequest.Messages[2]; got.Role != goagent.RoleUser || got.Content != "second" {
		t.Fatalf("second user message = %+v", got)
	}

	loaded, err := store.LoadSession(context.Background(), "conversation-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 4 {
		t.Fatalf("stored messages = %+v", loaded.Messages)
	}
}

func TestRunnerPersistsToolResultsInSession(t *testing.T) {
	store := goagent.NewMemorySessionStore()
	model := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}},
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "Clear."}, StopReason: goagent.StopComplete},
	}}
	tool, err := goagent.NewTool("weather", "Get weather.", func(context.Context, string) (string, error) {
		return "clear in Austin", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{tool}, SessionStore: store})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runner.Run(context.Background(), goagent.RunRequest{SessionID: "conversation-1", Input: "weather"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.LoadSession(context.Background(), "conversation-1")
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, message := range loaded.Messages {
		if message.Role == goagent.RoleTool && message.ToolCallID == "call-1" && message.Content == "clear in Austin" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("stored session missing tool result: %+v", loaded.Messages)
	}
}

func TestRunnerPreservesBlockJSONToolResultsAcrossPolicyEventsAndSessions(t *testing.T) {
	store := goagent.NewMemorySessionStore()
	wantJSON := map[string]any{
		"forecast": []any{map[string]any{"city": "Austin", "temp_c": float64(27)}},
		"ok":       true,
	}
	model := &recordingModel{turns: []goagent.TurnResult{
		{ToolCalls: []goagent.ToolCall{{ID: "call-1", Name: "weather", Input: json.RawMessage(`{"city":"Austin"}`)}}},
		{Message: goagent.Message{Role: goagent.RoleAssistant, Content: "Clear."}, Usage: goagent.Usage{InputTokens: 3, OutputTokens: 2, RequestID: "req-1"}, StopReason: goagent.StopComplete},
	}}
	var toolResultDecision goagent.Decision
	var stopDecision goagent.Decision
	policy := goagent.PolicyFunc(func(_ context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
		switch decision.Kind {
		case goagent.DecisionToolResult:
			toolResultDecision = decision
		case goagent.DecisionStop:
			stopDecision = decision
		}
		return goagent.PolicyDecision{Allowed: true}, nil
	})
	runner, err := goagent.NewRunner(goagent.Agent{Model: model, Tools: []goagent.Tool{jsonResultTool{value: wantJSON}}, Policy: policy, SessionStore: store})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{SessionID: "conversation-1", Input: "weather"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Usage.RequestID != "req-1" || result.StopReason != goagent.StopComplete {
		t.Fatalf("result metadata = %+v", result)
	}
	if toolResultDecision.Kind != goagent.DecisionToolResult || toolResultDecision.ToolCall.ID != "call-1" || toolResultDecision.Message.Role != goagent.RoleTool {
		t.Fatalf("tool result policy decision = %+v", toolResultDecision)
	}
	assertToolResultBlockJSON(t, toolResultDecision.Message, wantJSON)
	if stopDecision.Kind != goagent.DecisionStop || stopDecision.StopReason != goagent.StopComplete || len(stopDecision.Events) == 0 {
		t.Fatalf("stop policy decision = %+v", stopDecision)
	}
	if last := stopDecision.Events[len(stopDecision.Events)-1]; last.Kind == goagent.EventStop {
		t.Fatalf("stop decision included terminal stop event: %+v", last)
	}

	assembled, err := goagent.AssembleStream(result.Events, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(assembled.ToolResults) != 1 || !reflect.DeepEqual(assembled.ToolResults[0].JSON, wantJSON) {
		t.Fatalf("assembled tool results = %+v", assembled.ToolResults)
	}

	loaded, err := store.LoadSession(context.Background(), "conversation-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Messages) != 4 {
		t.Fatalf("stored messages = %+v", loaded.Messages)
	}
	assertToolResultBlockJSON(t, loaded.Messages[2], wantJSON)
	loaded.Messages[2].Blocks[0].ToolResult.JSON.(map[string]any)["ok"] = false
	reloaded, err := store.LoadSession(context.Background(), "conversation-1")
	if err != nil {
		t.Fatal(err)
	}
	assertToolResultBlockJSON(t, reloaded.Messages[2], wantJSON)

	_, err = runner.Run(context.Background(), goagent.RunRequest{SessionID: "conversation-1", Input: "again"})
	if err == nil {
		t.Fatal("third run unexpectedly succeeded after model turns were exhausted")
	}
	thirdRequest := model.requests[2]
	if len(thirdRequest.Messages) < 3 {
		t.Fatalf("resumed messages = %+v", thirdRequest.Messages)
	}
	assertToolResultBlockJSON(t, thirdRequest.Messages[2], wantJSON)
}

func TestRunnerPropagatesSessionStoreErrors(t *testing.T) {
	wantErr := errors.New("store unavailable")
	runner, err := goagent.NewRunner(goagent.Agent{
		Model:        &recordingModel{turns: []goagent.TurnResult{{Message: goagent.Message{Content: "unreached"}}}},
		SessionStore: failingSessionStore{loadErr: wantErr},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), goagent.RunRequest{SessionID: "conversation-1"}); !errors.Is(err, wantErr) {
		t.Fatalf("load error = %v, want %v", err, wantErr)
	}

	runner, err = goagent.NewRunner(goagent.Agent{
		Model:        &recordingModel{turns: []goagent.TurnResult{{Message: goagent.Message{Content: "done"}, StopReason: goagent.StopComplete}}},
		SessionStore: failingSessionStore{saveErr: wantErr},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(context.Background(), goagent.RunRequest{SessionID: "conversation-1"}); !errors.Is(err, wantErr) {
		t.Fatalf("save error = %v, want %v", err, wantErr)
	}
}

type failingSessionStore struct {
	loadErr error
	saveErr error
}

type jsonResultTool struct {
	value map[string]any
}

func (jsonResultTool) Name() string { return "weather" }

func (jsonResultTool) Description() string { return "Get structured weather." }

func (jsonResultTool) Schema() goagent.ToolSchema { return goagent.ToolSchema{"type": "object"} }

func (t jsonResultTool) Call(context.Context, goagent.ToolCall) (goagent.ToolResult, error) {
	return goagent.ToolResult{CallID: "call-1", Name: "weather", JSON: t.value}, nil
}

func assertToolResultBlockJSON(t *testing.T, message goagent.Message, want map[string]any) {
	t.Helper()
	if message.Role != goagent.RoleTool || message.ToolCallID != "call-1" || len(message.Blocks) != 1 {
		t.Fatalf("tool message = %+v", message)
	}
	block := message.Blocks[0]
	if block.ID != "tool-result-call-1" || block.Kind != goagent.BlockToolResult || block.ToolResult.CallID != "call-1" || block.ToolResult.Name != "weather" {
		t.Fatalf("tool result block = %+v", block)
	}
	if !reflect.DeepEqual(block.ToolResult.JSON, want) {
		t.Fatalf("tool JSON = %+v, want %+v", block.ToolResult.JSON, want)
	}
}

func (s failingSessionStore) LoadSession(context.Context, string) (goagent.Session, error) {
	if s.loadErr != nil {
		return goagent.Session{}, s.loadErr
	}
	return goagent.Session{ID: "conversation-1"}, nil
}

func (s failingSessionStore) SaveSession(_ context.Context, session goagent.Session) error {
	return s.saveErr
}
