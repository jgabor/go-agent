package goagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

const persistedEventsVersion = 1

// persistedEventsEnvelope is the on-disk JSON wrapper for event batches.
type persistedEventsEnvelope struct {
	V      int         `json:"v"`
	Events []JSONEvent `json:"events"`
	Schema string      `json:"schema,omitempty"`
}

// JSONEvent is a JSON-serializable snapshot of Event. It omits the Go error
// interface in favor of ErrorMessage and ErrorType strings.
type JSONEvent struct {
	V int `json:"v,omitempty"`

	Sequence    int64             `json:"seq,omitempty"`
	Kind        EventKind         `json:"kind,omitempty"`
	RunID       string            `json:"runId,omitempty"`
	ParentRunID string            `json:"parentRunId,omitempty"`
	TaskID      string            `json:"taskId,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`

	ErrorMessage string `json:"errorMessage,omitempty"`
	ErrorType    string `json:"errorType,omitempty"`

	TurnID         string              `json:"turnId,omitempty"`
	MessageID      string              `json:"messageId,omitempty"`
	BlockID        string              `json:"blockId,omitempty"`
	BlockKind      BlockKind           `json:"blockKind,omitempty"`
	ToolCallID     string              `json:"toolCallId,omitempty"`
	Text           string              `json:"text,omitempty"`
	Message        Message             `json:"message,omitempty"`
	Tool           ToolSpec            `json:"tool,omitempty"`
	ToolCall       ToolCall            `json:"toolCall,omitempty"`
	ToolCallDelta  ToolCallDelta       `json:"toolCallDelta,omitempty"`
	ToolResult     ToolResult          `json:"toolResult,omitempty"`
	ToolProgress   ToolProgress        `json:"toolProgress,omitempty"`
	Usage          Usage               `json:"usage,omitempty"`
	Diagnostics    ProviderDiagnostics `json:"diagnostics,omitempty"`
	Decision       *jsonDecision       `json:"decision,omitempty"`
	PolicyDecision *PolicyDecision     `json:"policyDecision,omitempty"`
	Retry          RetryEvent          `json:"retry,omitempty"`
	StopReason     StopReason          `json:"stopReason,omitempty"`
}

type jsonDecision struct {
	Kind       DecisionKind     `json:"kind"`
	Request    RunRequest       `json:"request"`
	ToolCall   ToolCall         `json:"toolCall"`
	Tool       ToolSpec         `json:"tool"`
	ToolResult ToolResult       `json:"toolResult"`
	Message    Message          `json:"message"`
	Retry      jsonRetryContext `json:"retry"`
	Session    Session          `json:"session"`
	StopReason StopReason       `json:"stopReason"`
	Events     []JSONEvent      `json:"events,omitempty"`
}

type jsonRetryContext struct {
	Target      RetryTarget `json:"target"`
	Reason      RetryReason `json:"reason"`
	Attempt     int         `json:"attempt"`
	MaxAttempts int         `json:"maxAttempts"`
	Request     RunRequest  `json:"request"`
	Session     Session     `json:"session"`
	TurnID      string      `json:"turnId"`
	ToolCall    ToolCall    `json:"toolCall"`
	Tool        ToolSpec    `json:"tool"`
	ErrMessage  string      `json:"errMessage,omitempty"`
	ErrType     string      `json:"errType,omitempty"`
}

// ToJSONEvent converts a runtime Event into its JSON projection.
func ToJSONEvent(e Event) JSONEvent {
	j := JSONEvent{
		V:             1,
		Sequence:      e.Sequence,
		Kind:          e.Kind,
		RunID:         e.RunID,
		ParentRunID:   e.ParentRunID,
		TaskID:        e.TaskID,
		Metadata:      cloneStringMap(e.Metadata),
		TurnID:        e.TurnID,
		MessageID:     e.MessageID,
		BlockID:       e.BlockID,
		BlockKind:     e.BlockKind,
		ToolCallID:    e.ToolCallID,
		Text:          e.Text,
		Message:       cloneMessage(e.Message),
		Tool:          cloneToolSpec(e.Tool),
		ToolCall:      e.ToolCall,
		ToolCallDelta: e.ToolCallDelta,
		ToolResult:    cloneToolResult(e.ToolResult),
		ToolProgress:  cloneToolProgress(e.ToolProgress),
		Usage:         e.Usage,
		Diagnostics:   e.Diagnostics,
		Retry:         e.Retry,
		StopReason:    e.StopReason,
	}
	if e.Err != nil {
		j.ErrorMessage = e.Err.Error()
		j.ErrorType = fmt.Sprintf("%T", e.Err)
	}
	if jd := toJSONDecision(e.Decision); jd != nil {
		j.Decision = jd
	}
	if e.Kind == EventPolicyDecision {
		pd := clonePolicyDecision(e.PolicyDecision)
		j.PolicyDecision = &pd
	}
	return j
}

func clonePolicyDecision(p PolicyDecision) PolicyDecision {
	if p.ToolCall != nil {
		tc := *p.ToolCall
		p.ToolCall = &tc
	}
	if p.ToolResult != nil {
		tr := cloneToolResult(*p.ToolResult)
		p.ToolResult = &tr
	}
	return p
}

func toJSONDecision(d Decision) *jsonDecision {
	if d.Kind == "" {
		return nil
	}
	j := jsonDecision{
		Kind:       d.Kind,
		Request:    cloneJSONRunRequest(d.Request),
		ToolCall:   d.ToolCall,
		Tool:       cloneToolSpec(d.Tool),
		ToolResult: cloneToolResult(d.ToolResult),
		Message:    cloneMessage(d.Message),
		Session:    cloneJSONSession(d.Session),
		StopReason: d.StopReason,
		Retry:      toJSONRetryContext(d.Retry),
	}
	if len(d.Events) > 0 {
		j.Events = make([]JSONEvent, len(d.Events))
		for i := range d.Events {
			j.Events[i] = ToJSONEvent(d.Events[i])
		}
	}
	return &j
}

func toJSONRetryContext(r RetryContext) jsonRetryContext {
	j := jsonRetryContext{
		Target:      r.Target,
		Reason:      r.Reason,
		Attempt:     r.Attempt,
		MaxAttempts: r.MaxAttempts,
		Request:     cloneJSONRunRequest(r.Request),
		Session:     cloneJSONSession(r.Session),
		TurnID:      r.TurnID,
		ToolCall:    r.ToolCall,
		Tool:        cloneToolSpec(r.Tool),
	}
	if r.Err != nil {
		j.ErrMessage = r.Err.Error()
		j.ErrType = fmt.Sprintf("%T", r.Err)
	}
	return j
}

func cloneJSONRunRequest(r RunRequest) RunRequest {
	r = cloneRunRequest(r)
	r.Session = cloneJSONSession(r.Session)
	return r
}

func cloneJSONSession(session Session) Session {
	clone := session
	clone.Messages = cloneMessages(session.Messages)
	clone.Values = cloneJSONValueMap(session.Values)
	return clone
}

func cloneJSONValueMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	clone := make(map[string]any, len(values))
	for key, value := range values {
		safe, ok := jsonSafeValue(value)
		if !ok {
			continue
		}
		clone[key] = safe
	}
	if len(clone) == 0 && len(values) > 0 {
		return nil
	}
	return clone
}

func jsonSafeValue(value any) (any, bool) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, false
	}
	return out, true
}

// FromJSONEvent converts JSONEvent back into a runtime Event.
func FromJSONEvent(j JSONEvent) Event {
	e := Event{
		Sequence:      j.Sequence,
		Kind:          j.Kind,
		RunID:         j.RunID,
		ParentRunID:   j.ParentRunID,
		TaskID:        j.TaskID,
		Metadata:      cloneStringMap(j.Metadata),
		TurnID:        j.TurnID,
		MessageID:     j.MessageID,
		BlockID:       j.BlockID,
		BlockKind:     j.BlockKind,
		ToolCallID:    j.ToolCallID,
		Text:          j.Text,
		Message:       cloneMessage(j.Message),
		Tool:          cloneToolSpec(j.Tool),
		ToolCall:      j.ToolCall,
		ToolCallDelta: j.ToolCallDelta,
		ToolResult:    cloneToolResult(j.ToolResult),
		ToolProgress:  cloneToolProgress(j.ToolProgress),
		Usage:         j.Usage,
		Diagnostics:   j.Diagnostics,
		Retry:         j.Retry,
		StopReason:    j.StopReason,
	}
	if j.ErrorMessage != "" {
		e.Err = errors.New(j.ErrorMessage)
	}
	if j.Decision != nil {
		e.Decision = fromJSONDecision(*j.Decision)
	}
	if j.PolicyDecision != nil {
		pd := clonePolicyDecision(*j.PolicyDecision)
		if pd.ToolCall != nil {
			normalizeToolCallInPlace(pd.ToolCall)
		}
		e.PolicyDecision = pd
	}
	normalizeToolCallInPlace(&e.ToolCall)
	return e
}

// JSON null for json.RawMessage unmarshals as the literal bytes "null"; treat
// that as absent input so persistence round-trips match in-memory zero values.
func normalizeToolCallInPlace(tc *ToolCall) {
	if tc == nil {
		return
	}
	if len(tc.Input) == 0 {
		tc.Input = nil
		return
	}
	if bytes.Equal(tc.Input, []byte("null")) {
		tc.Input = nil
	}
}

func fromJSONDecision(j jsonDecision) Decision {
	d := Decision{
		Kind:       j.Kind,
		Request:    cloneRunRequest(j.Request),
		ToolCall:   j.ToolCall,
		Tool:       cloneToolSpec(j.Tool),
		ToolResult: cloneToolResult(j.ToolResult),
		Message:    cloneMessage(j.Message),
		Session:    cloneSession(j.Session),
		StopReason: j.StopReason,
		Retry:      fromJSONRetryContext(j.Retry),
	}
	if len(j.Events) > 0 {
		d.Events = make([]Event, len(j.Events))
		for i := range j.Events {
			d.Events[i] = FromJSONEvent(j.Events[i])
		}
	}
	normalizeToolCallInPlace(&d.ToolCall)
	return d
}

func fromJSONRetryContext(j jsonRetryContext) RetryContext {
	r := RetryContext{
		Target:      j.Target,
		Reason:      j.Reason,
		Attempt:     j.Attempt,
		MaxAttempts: j.MaxAttempts,
		Request:     cloneRunRequest(j.Request),
		Session:     cloneSession(j.Session),
		TurnID:      j.TurnID,
		ToolCall:    j.ToolCall,
		Tool:        cloneToolSpec(j.Tool),
	}
	if j.ErrMessage != "" {
		r.Err = errors.New(j.ErrMessage)
	}
	normalizeToolCallInPlace(&r.ToolCall)
	return r
}

// MarshalEvents serializes events to JSON using the versioned envelope format.
func MarshalEvents(events []Event) ([]byte, error) {
	wrap := persistedEventsEnvelope{
		V:      persistedEventsVersion,
		Events: make([]JSONEvent, len(events)),
		Schema: "github.com/jgabor/go-agent/events@v1",
	}
	for i, e := range events {
		wrap.Events[i] = ToJSONEvent(e)
	}
	return json.Marshal(wrap)
}

// UnmarshalEvents deserializes JSON produced by MarshalEvents.
func UnmarshalEvents(data []byte) ([]Event, error) {
	var wrap persistedEventsEnvelope
	if err := json.Unmarshal(data, &wrap); err != nil {
		return nil, fmt.Errorf("goagent: unmarshal events: %w", err)
	}
	if wrap.V != persistedEventsVersion {
		return nil, fmt.Errorf("goagent: unsupported events format version %d", wrap.V)
	}
	out := make([]Event, len(wrap.Events))
	for i, j := range wrap.Events {
		out[i] = FromJSONEvent(j)
	}
	return out, nil
}
