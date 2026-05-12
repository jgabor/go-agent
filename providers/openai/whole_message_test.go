package openai

import (
	"encoding/json"
	"strings"
	"testing"

	goagent "github.com/jgabor/go-agent"
)

func TestDecodeChatCompletionResponsePreservesHiddenReasoningForReplay(t *testing.T) {
	const hiddenReasoning = "bounded whole-message reasoning fixture"
	data := []byte(`{
		"id":"resp-1",
		"model":"gpt-test",
		"choices":[{
			"index":0,
			"message":{
				"role":"assistant",
				"content":"Visible answer.",
				"reasoning_content":"bounded whole-message reasoning fixture",
				"tool_calls":[{"id":"call-1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Austin\"}"}}]
			},
			"finish_reason":"tool_calls"
		}],
		"usage":{"prompt_tokens":3,"completion_tokens":5,"total_tokens":8}
	}`)

	turn, err := decodeChatCompletionResponse(data)
	if err != nil {
		t.Fatal(err)
	}
	if turn.Message.Content != "Visible answer." || turn.Message.HiddenReasoning != hiddenReasoning || len(turn.Message.ToolCalls) != 1 {
		t.Fatalf("decoded message = %+v", turn.Message)
	}
	if turn.Message.ToolCalls[0].Name != "weather" || string(turn.Message.ToolCalls[0].Input) != `{"city":"Austin"}` {
		t.Fatalf("decoded tool call = %+v", turn.Message.ToolCalls[0])
	}
	requestMessages := openAIMessages(goagent.TurnRequest{Messages: []goagent.Message{turn.Message}})
	if len(requestMessages) != 1 || requestMessages[0].ReasoningContent != hiddenReasoning {
		t.Fatalf("replay message = %+v", requestMessages)
	}

	var events []goagent.Event
	goagent.StreamTurnResult(turn, func(event goagent.Event) {
		events = append(events, event)
	})
	events = append(events, goagent.Event{Kind: goagent.EventStop, StopReason: goagent.StopComplete})
	assembled, err := goagent.AssembleEvents(events)
	if err != nil {
		t.Fatal(err)
	}
	if assembled.Text != "Visible answer." || assembled.Messages[0].HiddenReasoning != hiddenReasoning {
		t.Fatalf("assembled = %+v", assembled)
	}
	for _, event := range events {
		if event.Kind == goagent.EventTextDelta && strings.Contains(event.Text, "reasoning") {
			t.Fatalf("hidden reasoning leaked into text event: %+v", event)
		}
	}
	marshaled, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(marshaled), hiddenReasoning) || strings.Contains(assembled.Text, "reasoning") {
		t.Fatalf("hidden reasoning leaked into visible projections: text=%q events=%s", assembled.Text, marshaled)
	}
}
