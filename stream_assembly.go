package goagent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// AssembledRun is the deterministic completion projection of canonical events.
type AssembledRun struct {
	Messages    []Message
	ToolCalls   []ToolCall
	ToolResults []ToolResult
	Usage       Usage
	StopReason  StopReason
	Text        string
	Err         error
}

// StreamDivergenceError reports a malformed or contradictory event stream.
type StreamDivergenceError struct {
	EventIndex int
	Reason     string
}

func (e StreamDivergenceError) Error() string {
	if e.EventIndex < 0 {
		return "goagent: stream divergence: " + e.Reason
	}
	return fmt.Sprintf("goagent: stream divergence at event %d: %s", e.EventIndex, e.Reason)
}

// AssembleStream reduces stored events and applies completion-style Go error semantics.
//
// A setup error before accepted events is returned as-is. Once events exist, a
// stream error must be represented by terminal error and stop events so replay
// consumers observe the same terminal state.
func AssembleStream(events []Event, streamErr error) (AssembledRun, error) {
	assembled, err := AssembleEvents(events)
	if err != nil {
		return assembled, err
	}
	if streamErr == nil {
		if assembled.Err != nil {
			return assembled, assembled.Err
		}
		return assembled, nil
	}
	if len(events) == 0 {
		return assembled, streamErr
	}
	if assembled.Err == nil {
		return assembled, StreamDivergenceError{EventIndex: len(events) - 1, Reason: "accepted stream error missing terminal error event"}
	}
	if !sameStreamError(streamErr, assembled.Err) {
		return assembled, StreamDivergenceError{EventIndex: terminalErrorIndex(events), Reason: "stream error does not match terminal error event"}
	}
	return assembled, streamErr
}

// AssembleEvents deterministically reduces canonical events into final run state.
func AssembleEvents(events []Event) (AssembledRun, error) {
	reducer := eventReducer{blocks: map[string]*assembledBlock{}}
	for i, event := range events {
		if err := reducer.apply(i, event); err != nil {
			return reducer.result(), err
		}
	}
	return reducer.finish(len(events))
}

func assembleTurnEvents(events []Event) (AssembledRun, error) {
	reducer := eventReducer{blocks: map[string]*assembledBlock{}}
	for i, event := range events {
		if err := reducer.apply(i, event); err != nil {
			return reducer.result(), err
		}
	}
	return reducer.finishPartial(len(events))
}

type eventReducer struct {
	resultValue AssembledRun
	blocks      map[string]*assembledBlock
	blockOrder  []string
	started     bool
	final       bool
	terminalErr bool
	stopped     bool
	lastSeq     int64
}

type assembledBlock struct {
	id        string
	kind      BlockKind
	toolCall  ToolCall
	text      strings.Builder
	closed    bool
	order     int
	messageID string
}

func (r *eventReducer) apply(index int, event Event) error {
	if event.Sequence != 0 {
		if event.Sequence <= r.lastSeq {
			return divergence(index, "event sequence is not monotonically increasing")
		}
		r.lastSeq = event.Sequence
	}
	if r.stopped {
		return divergence(index, "event appears after terminal stop")
	}
	if r.terminalErr && event.Kind != EventUsage && event.Kind != EventStop {
		return divergence(index, "only usage and stop may follow terminal error")
	}

	switch event.Kind {
	case EventResponseStart:
		if r.started && !r.final {
			return divergence(index, "duplicate response_start")
		}
		r.started = true
		r.final = false
		r.blocks = map[string]*assembledBlock{}
		r.blockOrder = nil
	case EventContentBlockStart:
		return r.startBlock(index, event)
	case EventTextDelta:
		return r.textDelta(index, event)
	case EventToolCallDelta:
		return r.toolCallDelta(index, event)
	case EventContentBlockEnd:
		return r.endBlock(index, event)
	case EventMessageFinal:
		return r.finalMessage(index, event)
	case EventToolCallReady, EventToolCall:
		return r.toolCallReady(index, event)
	case EventToolResult:
		return r.toolResult(index, event)
	case EventUsage:
		r.resultValue.Usage = event.Usage
	case EventError:
		if r.terminalErr {
			return divergence(index, "duplicate terminal error")
		}
		if !r.started {
			return divergence(index, "terminal error before response_start")
		}
		if event.Err == nil {
			return divergence(index, "terminal error missing Go error")
		}
		r.terminalErr = true
		r.resultValue.Err = event.Err
	case EventStop:
		return r.stop(index, event)
	case EventToolProgress, EventPolicyPending, EventPolicyDecision, EventRetry:
		// Runtime decision events are observational for transcript assembly.
	default:
		return divergence(index, fmt.Sprintf("unknown event kind %q", event.Kind))
	}
	return nil
}

func (r *eventReducer) startBlock(index int, event Event) error {
	if !r.started {
		return divergence(index, "content block started before response_start")
	}
	if event.BlockID == "" {
		return divergence(index, "content block start missing block ID")
	}
	if _, exists := r.blocks[event.BlockID]; exists {
		return divergence(index, "duplicate content block start")
	}
	if event.BlockKind != BlockText && event.BlockKind != BlockToolCall {
		return divergence(index, "unsupported assistant content block kind")
	}
	r.blocks[event.BlockID] = &assembledBlock{id: event.BlockID, kind: event.BlockKind, toolCall: ToolCall{ID: event.ToolCallID}, order: len(r.blockOrder), messageID: event.MessageID}
	r.blockOrder = append(r.blockOrder, event.BlockID)
	return nil
}

func (r *eventReducer) textDelta(index int, event Event) error {
	block, err := r.openBlock(index, event.BlockID, BlockText)
	if err != nil {
		return err
	}
	block.text.WriteString(event.Text)
	return nil
}

func (r *eventReducer) toolCallDelta(index int, event Event) error {
	block, err := r.openBlock(index, event.BlockID, BlockToolCall)
	if err != nil {
		return err
	}
	if event.ToolCallID == "" {
		return divergence(index, "tool_call_delta missing tool call ID")
	}
	if block.toolCall.ID == "" {
		block.toolCall.ID = event.ToolCallID
	}
	if block.toolCall.ID != event.ToolCallID {
		return divergence(index, "tool_call_delta changed tool call ID")
	}
	block.toolCall.Name += event.ToolCallDelta.NameDelta
	block.toolCall.Input = append(block.toolCall.Input, event.ToolCallDelta.ArgumentsDelta...)
	return nil
}

func (r *eventReducer) endBlock(index int, event Event) error {
	block, ok := r.blocks[event.BlockID]
	if !ok {
		return divergence(index, "content block end for unknown block")
	}
	if block.closed {
		return divergence(index, "duplicate content block end")
	}
	if event.BlockKind != "" && event.BlockKind != block.kind {
		return divergence(index, "content block end changed block kind")
	}
	if block.kind == BlockText && event.Text != "" && event.Text != block.text.String() {
		return divergence(index, "content block end contradicts text deltas")
	}
	if block.kind == BlockToolCall {
		if event.ToolCallID != "" && event.ToolCallID != block.toolCall.ID {
			return divergence(index, "content block end changed tool call ID")
		}
		if event.ToolCall.ID != "" && !sameToolCall(event.ToolCall, block.toolCall) {
			return divergence(index, "content block end contradicts tool call deltas")
		}
		if block.toolCall.Name == "" {
			return divergence(index, "tool call ended with empty name")
		}
		if !json.Valid(block.toolCall.Input) {
			return divergence(index, "tool call ended with invalid JSON arguments")
		}
	}
	block.closed = true
	return nil
}

func (r *eventReducer) finalMessage(index int, event Event) error {
	if r.final {
		return divergence(index, "duplicate message_final")
	}
	for _, id := range r.blockOrder {
		if !r.blocks[id].closed {
			return divergence(index, "message_final before content block end")
		}
	}
	message := Message{Role: RoleAssistant, Blocks: r.messageBlocks()}
	message.Content = finalText(message.Blocks)
	message.ToolCalls = finalToolCalls(message.Blocks)
	if !event.Message.empty() && !sameFinalMessage(event.Message, message) {
		return divergence(index, "message_final contradicts streamed blocks")
	}
	r.resultValue.Messages = append(r.resultValue.Messages, message)
	r.resultValue.Text += message.Content
	r.resultValue.ToolCalls = append(r.resultValue.ToolCalls, message.ToolCalls...)
	r.final = true
	return nil
}

func (r *eventReducer) toolCallReady(index int, event Event) error {
	if !r.final {
		return divergence(index, "tool_call_ready before message_final")
	}
	for _, call := range r.resultValue.ToolCalls {
		if call.ID == event.ToolCallID || call.ID == event.ToolCall.ID {
			if event.ToolCall.ID != "" && !sameToolCall(event.ToolCall, call) {
				return divergence(index, "tool_call_ready contradicts finalized tool call")
			}
			return nil
		}
	}
	return divergence(index, "tool_call_ready for unknown tool call")
}

func (r *eventReducer) toolResult(index int, event Event) error {
	result := event.ToolResult
	if result.CallID == "" {
		result.CallID = event.ToolCallID
	}
	if result.CallID == "" {
		return divergence(index, "tool_result missing call ID")
	}
	message := event.Message
	if message.empty() {
		message = Message{Role: RoleTool, ToolCallID: result.CallID, Blocks: []Block{{ID: event.BlockID, Kind: BlockToolResult, ToolResult: result}}}
		message.Content = result.Content
	}
	r.resultValue.Messages = append(r.resultValue.Messages, message)
	r.resultValue.ToolResults = append(r.resultValue.ToolResults, result)
	return nil
}

func (r *eventReducer) stop(index int, event Event) error {
	if event.StopReason == "" {
		return divergence(index, "stop missing reason")
	}
	if r.terminalErr && event.StopReason == StopComplete {
		return divergence(index, "terminal error cannot stop complete")
	}
	if !r.final && !r.terminalErr && len(r.blockOrder) > 0 {
		return divergence(index, "stop before message_final")
	}
	r.stopped = true
	r.resultValue.StopReason = event.StopReason
	return nil
}

func (r *eventReducer) finish(index int) (AssembledRun, error) {
	if (r.started || r.terminalErr) && !r.stopped {
		return r.result(), divergence(index, "stream ended before terminal stop")
	}
	return r.result(), nil
}

func (r *eventReducer) finishPartial(index int) (AssembledRun, error) {
	if r.terminalErr && !r.stopped {
		return r.result(), divergence(index, "stream ended before terminal stop")
	}
	if r.started && !r.final && len(r.blockOrder) > 0 {
		return r.result(), divergence(index, "stream ended before message_final")
	}
	return r.result(), nil
}

func (r *eventReducer) result() AssembledRun {
	result := r.resultValue
	result.Messages = append([]Message(nil), result.Messages...)
	result.ToolCalls = append([]ToolCall(nil), result.ToolCalls...)
	result.ToolResults = append([]ToolResult(nil), result.ToolResults...)
	return result
}

func (r *eventReducer) openBlock(index int, id string, kind BlockKind) (*assembledBlock, error) {
	block, ok := r.blocks[id]
	if !ok {
		return nil, divergence(index, "delta for unknown block")
	}
	if block.closed {
		return nil, divergence(index, "delta after content block end")
	}
	if block.kind != kind {
		return nil, divergence(index, "delta kind does not match block kind")
	}
	return block, nil
}

func (r *eventReducer) messageBlocks() []Block {
	blocks := make([]Block, 0, len(r.blockOrder))
	for _, id := range r.blockOrder {
		block := r.blocks[id]
		assembled := Block{ID: block.id, Kind: block.kind}
		if block.kind == BlockText {
			assembled.Text = block.text.String()
		} else {
			assembled.ToolCall = block.toolCall
		}
		blocks = append(blocks, assembled)
	}
	return blocks
}

func divergence(index int, reason string) StreamDivergenceError {
	return StreamDivergenceError{EventIndex: index, Reason: reason}
}

func finalText(blocks []Block) string {
	var text strings.Builder
	for _, block := range blocks {
		if block.Kind == BlockText {
			text.WriteString(block.Text)
		}
	}
	return text.String()
}

func finalToolCalls(blocks []Block) []ToolCall {
	var calls []ToolCall
	for _, block := range blocks {
		if block.Kind == BlockToolCall {
			calls = append(calls, block.ToolCall)
		}
	}
	return calls
}

func sameFinalMessage(got Message, want Message) bool {
	if got.Role != "" && got.Role != want.Role {
		return false
	}
	if got.Content != "" && got.Content != want.Content {
		return false
	}
	if len(got.Blocks) > 0 && !reflect.DeepEqual(got.Blocks, want.Blocks) {
		return false
	}
	if len(got.ToolCalls) > 0 && !sameToolCalls(got.ToolCalls, want.ToolCalls) {
		return false
	}
	return true
}

func sameToolCalls(got []ToolCall, want []ToolCall) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !sameToolCall(got[i], want[i]) {
			return false
		}
	}
	return true
}

func sameToolCall(got ToolCall, want ToolCall) bool {
	return got.ID == want.ID && got.Name == want.Name && bytes.Equal(got.Input, want.Input)
}

func sameStreamError(got error, want error) bool {
	return errors.Is(got, want) || errors.Is(want, got)
}

func terminalErrorIndex(events []Event) int {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Kind == EventError {
			return i
		}
	}
	return len(events) - 1
}

func (m Message) empty() bool {
	return m.Role == "" && m.Content == "" && len(m.Blocks) == 0 && m.Name == "" && m.ToolCallID == "" && len(m.ToolCalls) == 0
}

// IsStreamDivergence reports whether err is a deterministic reducer divergence.
func IsStreamDivergence(err error) bool {
	var divergence StreamDivergenceError
	return errors.As(err, &divergence)
}
