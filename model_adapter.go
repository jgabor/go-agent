package goagent

import (
	"context"
	"fmt"
)

type simpleModelAdapter struct {
	model SimpleModel
}

func (m simpleModelAdapter) Stream(ctx context.Context, request TurnRequest, emit func(Event)) error {
	if m.model == nil {
		return fmt.Errorf("goagent: simple model is nil")
	}
	turn, err := m.model.Turn(ctx, request)
	if err != nil {
		return err
	}
	StreamTurnResult(turn, emit)
	return nil
}

// StreamTurnResult converts a final SimpleModel response into canonical stream events.
func StreamTurnResult(turn TurnResult, emit func(Event)) {
	if emit == nil {
		return
	}
	message := finalTurnMessage(turn)
	emit(Event{Kind: EventResponseStart, MessageID: "message-1"})
	for i, block := range message.Blocks {
		if block.ID == "" {
			block.ID = fmt.Sprintf("block-%d", i+1)
			message.Blocks[i].ID = block.ID
		}
		event := Event{Kind: EventContentBlockStart, MessageID: "message-1", BlockID: block.ID, BlockKind: block.Kind, Diagnostics: turn.Diagnostics}
		if block.Kind == BlockToolCall {
			event.ToolCallID = block.ToolCall.ID
		}
		emit(event)
		switch block.Kind {
		case BlockText:
			if block.Text != "" {
				emit(Event{Kind: EventTextDelta, BlockID: block.ID, Text: block.Text})
			}
			emit(Event{Kind: EventContentBlockEnd, BlockID: block.ID, BlockKind: BlockText, Text: block.Text})
		case BlockToolCall:
			emit(Event{Kind: EventToolCallDelta, BlockID: block.ID, ToolCallID: block.ToolCall.ID, ToolCallDelta: ToolCallDelta{NameDelta: block.ToolCall.Name, ArgumentsDelta: string(block.ToolCall.Input)}})
			emit(Event{Kind: EventContentBlockEnd, BlockID: block.ID, BlockKind: BlockToolCall, ToolCallID: block.ToolCall.ID, ToolCall: block.ToolCall})
		}
	}
	emit(Event{Kind: EventMessageFinal, MessageID: "message-1", Message: message, Diagnostics: turn.Diagnostics})
	for _, call := range message.ToolCalls {
		emit(Event{Kind: EventToolCallReady, ToolCallID: call.ID, ToolCall: call})
	}
	if !turn.Usage.empty() {
		emit(Event{Kind: EventUsage, Usage: turn.Usage})
	}
}

func finalTurnMessage(turn TurnResult) Message {
	message := turn.Message
	if message.Role == "" {
		message.Role = RoleAssistant
	}
	if len(message.ToolCalls) == 0 {
		message.ToolCalls = append([]ToolCall(nil), turn.ToolCalls...)
	}
	if len(message.Blocks) == 0 {
		if message.Content != "" {
			message.Blocks = append(message.Blocks, Block{Kind: BlockText, Text: message.Content})
		}
		for _, call := range message.ToolCalls {
			message.Blocks = append(message.Blocks, Block{Kind: BlockToolCall, ToolCall: call})
		}
	}
	if message.Content == "" {
		message.Content = finalText(message.Blocks)
	}
	if len(message.ToolCalls) == 0 {
		message.ToolCalls = finalToolCalls(message.Blocks)
	}
	return message
}

func (u Usage) empty() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 && u.TotalTokens == 0 && u.CachedInputTokens == 0 && u.CacheWriteTokens == 0 && u.ReasoningTokens == 0 && u.RequestID == "" && u.Provider == "" && u.Model == ""
}
