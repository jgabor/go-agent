package openai_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jgabor/go-agent/providers/openai"
)

func TestOpenAICompatibleStreamingProofFitsDraftGrammar(t *testing.T) {
	providerErr := errors.New("provider stream failed")
	tests := []struct {
		name      string
		steps     []proofWireStep
		wantKinds []proofEventKind
		wantErr   bool
	}{
		{
			name: "streaming text usage and stop finish reason",
			steps: []proofWireStep{
				{RequestID: "req_text", TextDelta: "Bring "},
				{RequestID: "req_text", TextDelta: "a jacket."},
				{RequestID: "req_text", Usage: &proofUsage{InputTokens: 9, OutputTokens: 4, TotalTokens: 13}, FinishReason: "stop"},
			},
			wantKinds: []proofEventKind{proofResponseStart, proofContentBlockStart, proofTextDelta, proofTextDelta, proofContentBlockEnd, proofMessageFinal, proofUsageEvent, proofStop},
		},
		{
			name: "streaming tool call by index with finish reason",
			steps: []proofWireStep{
				{RequestID: "req_tool", ToolIndex: ptr(0), ToolID: "call_1", ToolNameDelta: "weath"},
				{RequestID: "req_tool", ToolIndex: ptr(0), ToolNameDelta: "er", ToolArgumentsDelta: `{"city"`},
				{RequestID: "req_tool", ToolIndex: ptr(0), ToolArgumentsDelta: `:"Austin"}`},
				{RequestID: "req_tool", Usage: &proofUsage{InputTokens: 12, OutputTokens: 8, TotalTokens: 20}, FinishReason: "tool_calls"},
			},
			wantKinds: []proofEventKind{proofResponseStart, proofContentBlockStart, proofToolCallDelta, proofToolCallDelta, proofToolCallDelta, proofContentBlockEnd, proofMessageFinal, proofToolCallReady, proofUsageEvent, proofStop},
		},
		{
			name: "setup failure returns go error before accepted stream",
			steps: []proofWireStep{
				{SetupErr: errors.New("missing api key")},
			},
			wantErr: true,
		},
		{
			name: "accepted provider failure emits terminal error usage and stop",
			steps: []proofWireStep{
				{RequestID: "req_err", TextDelta: "partial"},
				{RequestID: "req_err", HTTPStatus: 429, ErrorType: "rate_limit_error", ErrorCode: "rate_limit", AcceptedErr: providerErr, Usage: &proofUsage{InputTokens: 7, OutputTokens: 1, TotalTokens: 8}, Excerpt: "rate limit"},
			},
			wantKinds: []proofEventKind{proofResponseStart, proofContentBlockStart, proofTextDelta, proofErrorEvent, proofUsageEvent, proofStop},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, err := normalizeProofOpenAICompatibleStream(tt.steps)
			if tt.wantErr && err == nil {
				t.Fatal("error = nil, want error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("error = %v", err)
			}
			if got := proofEventKinds(events); !slices.Equal(got, tt.wantKinds) {
				t.Fatalf("event kinds = %v, want %v", got, tt.wantKinds)
			}
			assertProofEventsUseOnlyCanonicalFields(t, events)
			assertProofEventOrdering(t, events)
			assertProofDiagnosticsAreBounded(t, events)
		})
	}
}

func TestOpenAICompatibleProviderPackageKeepsProductPolicyOutOfCore(t *testing.T) {
	modelType := reflect.TypeOf(openai.ChatModel{})
	got := make([]string, 0, modelType.NumField())
	for i := range modelType.NumField() {
		got = append(got, modelType.Field(i).Name)
	}
	want := []string{"Model", "APIKey", "BaseURL", "HTTPClient", "Options"}
	if !slices.Equal(got, want) {
		t.Fatalf("ChatModel fields = %v, want %v", got, want)
	}

	for _, field := range got {
		lower := strings.ToLower(field)
		for _, forbidden := range []string{"registry", "providerselection", "marketplace", "discover", "authcache", "workdir", "pricing", "policy"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("ChatModel field %q contains forbidden core concern %q", field, forbidden)
			}
		}
	}
	optionsType := reflect.TypeOf(openai.ChatOptions{})
	for i := 0; i < optionsType.NumField(); i++ {
		if optionsType.Field(i).Type.Kind() == reflect.Map {
			t.Fatalf("ChatOptions field %q is an arbitrary pass-through map", optionsType.Field(i).Name)
		}
	}
}

type proofEventKind string

const (
	proofResponseStart     proofEventKind = "response_start"
	proofContentBlockStart proofEventKind = "content_block_start"
	proofTextDelta         proofEventKind = "text_delta"
	proofToolCallDelta     proofEventKind = "tool_call_delta"
	proofContentBlockEnd   proofEventKind = "content_block_end"
	proofMessageFinal      proofEventKind = "message_final"
	proofToolCallReady     proofEventKind = "tool_call_ready"
	proofUsageEvent        proofEventKind = "usage"
	proofStop              proofEventKind = "stop"
	proofErrorEvent        proofEventKind = "error"
)

type proofWireStep struct {
	RequestID          string
	TextDelta          string
	ToolIndex          *int
	ToolID             string
	ToolNameDelta      string
	ToolArgumentsDelta string
	FinishReason       string
	Usage              *proofUsage
	HTTPStatus         int
	ErrorType          string
	ErrorCode          string
	Excerpt            string
	SetupErr           error
	AcceptedErr        error
}

type proofEvent struct {
	Kind          proofEventKind
	MessageID     string
	BlockID       string
	ToolCallID    string
	BlockKind     string
	TextDelta     string
	ToolCallDelta proofToolCallDeltaPayload
	Message       proofMessage
	ToolCall      proofToolCall
	Usage         *proofUsage
	StopReason    string
	TerminalError error
	Diagnostics   proofDiagnostics
}

type proofToolCallDeltaPayload struct {
	Index          *int
	NameDelta      string
	ArgumentsDelta string
}

type proofMessage struct {
	Role   string
	Blocks []proofBlock
}

type proofBlock struct {
	ID       string
	Kind     string
	Text     string
	ToolCall proofToolCall
}

type proofToolCall struct {
	ID        string
	Name      string
	Arguments string
}

type proofUsage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

type proofDiagnostics struct {
	RequestID     string
	Provider      string
	Package       string
	HTTPStatus    int
	ErrorType     string
	ErrorCode     string
	RawStopReason string
	Excerpt       string
}

func normalizeProofOpenAICompatibleStream(steps []proofWireStep) ([]proofEvent, error) {
	if len(steps) == 1 && steps[0].SetupErr != nil {
		return nil, steps[0].SetupErr
	}

	state := proofStreamState{
		messageID: "msg_1",
		provider:  "openai-compatible",
		pkg:       "github.com/jgabor/go-agent/providers/openai",
		calls:     map[int]*proofToolCall{},
		blocks:    map[int]string{},
	}
	var terminalErr error
	for _, step := range steps {
		if step.SetupErr != nil {
			return nil, step.SetupErr
		}
		state.ensureStarted(step)
		if step.AcceptedErr != nil {
			state.events = append(state.events, proofEvent{Kind: proofErrorEvent, TerminalError: step.AcceptedErr, Diagnostics: state.diagnostics(step)})
			if step.Usage != nil {
				state.events = append(state.events, proofEvent{Kind: proofUsageEvent, Usage: step.Usage, Diagnostics: state.diagnostics(step)})
			}
			state.events = append(state.events, proofEvent{Kind: proofStop, StopReason: "model_error", Diagnostics: state.diagnostics(step)})
			terminalErr = step.AcceptedErr
			break
		}
		if step.TextDelta != "" {
			state.ensureTextBlock(step)
			state.text += step.TextDelta
			state.events = append(state.events, proofEvent{Kind: proofTextDelta, BlockID: state.textBlockID, TextDelta: step.TextDelta, Diagnostics: state.diagnostics(step)})
		}
		if step.ToolIndex != nil {
			state.ensureToolBlock(*step.ToolIndex, step)
			call := state.calls[*step.ToolIndex]
			if step.ToolID != "" {
				call.ID = step.ToolID
			}
			call.Name += step.ToolNameDelta
			call.Arguments += step.ToolArgumentsDelta
			state.events = append(state.events, proofEvent{
				Kind:       proofToolCallDelta,
				BlockID:    state.blocks[*step.ToolIndex],
				ToolCallID: call.ID,
				ToolCallDelta: proofToolCallDeltaPayload{
					Index:          step.ToolIndex,
					NameDelta:      step.ToolNameDelta,
					ArgumentsDelta: step.ToolArgumentsDelta,
				},
				Diagnostics: state.diagnostics(step),
			})
		}
		if step.FinishReason != "" {
			state.finalize(step)
		}
	}
	return state.events, terminalErr
}

type proofStreamState struct {
	events      []proofEvent
	started     bool
	messageID   string
	provider    string
	pkg         string
	textBlockID string
	text        string
	calls       map[int]*proofToolCall
	blocks      map[int]string
}

func (s *proofStreamState) ensureStarted(step proofWireStep) {
	if s.started {
		return
	}
	s.events = append(s.events, proofEvent{Kind: proofResponseStart, MessageID: s.messageID, Diagnostics: s.diagnostics(step)})
	s.started = true
}

func (s *proofStreamState) ensureTextBlock(step proofWireStep) {
	if s.textBlockID != "" {
		return
	}
	s.textBlockID = "block_text_1"
	s.events = append(s.events, proofEvent{Kind: proofContentBlockStart, MessageID: s.messageID, BlockID: s.textBlockID, BlockKind: "text", Diagnostics: s.diagnostics(step)})
}

func (s *proofStreamState) ensureToolBlock(index int, step proofWireStep) {
	if _, ok := s.calls[index]; ok {
		return
	}
	callID := step.ToolID
	if callID == "" {
		callID = "generated_call_" + string(rune('0'+index))
	}
	blockID := "block_tool_" + string(rune('0'+index))
	s.calls[index] = &proofToolCall{ID: callID}
	s.blocks[index] = blockID
	s.events = append(s.events, proofEvent{Kind: proofContentBlockStart, MessageID: s.messageID, BlockID: blockID, ToolCallID: callID, BlockKind: "tool_call", Diagnostics: s.diagnostics(step)})
}

func (s *proofStreamState) finalize(step proofWireStep) {
	blocks := make([]proofBlock, 0, 1+len(s.calls))
	if s.textBlockID != "" {
		s.events = append(s.events, proofEvent{Kind: proofContentBlockEnd, BlockID: s.textBlockID, BlockKind: "text", Diagnostics: s.diagnostics(step)})
		blocks = append(blocks, proofBlock{ID: s.textBlockID, Kind: "text", Text: s.text})
	}
	for index := 0; index < len(s.calls); index++ {
		call, ok := s.calls[index]
		if !ok {
			continue
		}
		blockID := s.blocks[index]
		s.events = append(s.events, proofEvent{Kind: proofContentBlockEnd, BlockID: blockID, ToolCallID: call.ID, BlockKind: "tool_call", ToolCall: *call, Diagnostics: s.diagnostics(step)})
		blocks = append(blocks, proofBlock{ID: blockID, Kind: "tool_call", ToolCall: *call})
	}
	message := proofMessage{Role: "assistant", Blocks: blocks}
	s.events = append(s.events, proofEvent{Kind: proofMessageFinal, MessageID: s.messageID, Message: message, Diagnostics: s.diagnostics(step)})
	for index := 0; index < len(s.calls); index++ {
		if call, ok := s.calls[index]; ok {
			s.events = append(s.events, proofEvent{Kind: proofToolCallReady, ToolCallID: call.ID, ToolCall: *call, Diagnostics: s.diagnostics(step)})
		}
	}
	if step.Usage != nil {
		s.events = append(s.events, proofEvent{Kind: proofUsageEvent, Usage: step.Usage, Diagnostics: s.diagnostics(step)})
	}
	s.events = append(s.events, proofEvent{Kind: proofStop, StopReason: proofStopReason(step.FinishReason), Diagnostics: s.diagnostics(step)})
}

func (s proofStreamState) diagnostics(step proofWireStep) proofDiagnostics {
	return proofDiagnostics{
		RequestID:     step.RequestID,
		Provider:      s.provider,
		Package:       s.pkg,
		HTTPStatus:    step.HTTPStatus,
		ErrorType:     step.ErrorType,
		ErrorCode:     step.ErrorCode,
		RawStopReason: step.FinishReason,
		Excerpt:       step.Excerpt,
	}
}

func proofStopReason(finishReason string) string {
	if finishReason == "stop" {
		return "complete"
	}
	if finishReason == "tool_calls" {
		return "tool_calls"
	}
	return "model_error"
}

func proofEventKinds(events []proofEvent) []proofEventKind {
	kinds := make([]proofEventKind, 0, len(events))
	for _, event := range events {
		kinds = append(kinds, event.Kind)
	}
	return kinds
}

func assertProofEventsUseOnlyCanonicalFields(t *testing.T, events []proofEvent) {
	t.Helper()
	if len(events) == 0 {
		return
	}
	eventType := reflect.TypeOf(events[0])
	for i := range eventType.NumField() {
		field := eventType.Field(i).Name
		lower := strings.ToLower(field)
		for _, forbidden := range []string{"openai", "choice", "deltaevent", "finishreason", "sse", "chunk"} {
			if strings.Contains(lower, forbidden) {
				t.Fatalf("proof event field %q is provider-specific", field)
			}
		}
	}
}

func assertProofEventOrdering(t *testing.T, events []proofEvent) {
	t.Helper()
	if len(events) == 0 {
		return
	}
	if events[0].Kind != proofResponseStart {
		t.Fatalf("first event = %q, want %q", events[0].Kind, proofResponseStart)
	}
	if events[len(events)-1].Kind != proofStop {
		t.Fatalf("last event = %q, want %q", events[len(events)-1].Kind, proofStop)
	}
	seenStop := false
	seenTerminalError := false
	for i, event := range events {
		if seenStop {
			t.Fatalf("event %d follows stop: %+v", i, event)
		}
		if seenTerminalError && event.Kind != proofUsageEvent && event.Kind != proofStop {
			t.Fatalf("event %d follows terminal error but is not usage or stop: %+v", i, event)
		}
		if event.Kind == proofErrorEvent {
			seenTerminalError = true
		}
		if event.Kind == proofStop {
			seenStop = true
		}
	}
}

func assertProofDiagnosticsAreBounded(t *testing.T, events []proofEvent) {
	t.Helper()
	for _, event := range events {
		if event.Diagnostics.Provider != "openai-compatible" {
			t.Fatalf("provider diagnostic = %q", event.Diagnostics.Provider)
		}
		if event.Diagnostics.Package != "github.com/jgabor/go-agent/providers/openai" {
			t.Fatalf("package diagnostic = %q", event.Diagnostics.Package)
		}
		for _, value := range []string{event.Diagnostics.RequestID, event.Diagnostics.Provider, event.Diagnostics.Package, event.Diagnostics.ErrorType, event.Diagnostics.ErrorCode, event.Diagnostics.RawStopReason, event.Diagnostics.Excerpt} {
			assertProofDiagnosticTextIsNonSecret(t, value)
		}
	}
}

func assertProofDiagnosticTextIsNonSecret(t *testing.T, value string) {
	t.Helper()
	lowerValue := strings.ToLower(strings.TrimSpace(value))
	for _, forbidden := range []string{"authorization", "api_key", "apikey", "bearer", "credential", "password", "prompt", "messages", "tool_args", "environment", "pricing", "marketplace", "registry", "lira_policy"} {
		if strings.Contains(lowerValue, forbidden) {
			t.Fatalf("diagnostic text exposes forbidden concern: %q", value)
		}
	}
}

func ptr[T any](value T) *T {
	return &value
}
