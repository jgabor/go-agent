// Package openai adapts OpenAI-compatible chat completion APIs to goagent.Model.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	goagent "github.com/jgabor/go-agent"
)

const defaultBaseURL = "https://api.openai.com/v1"

// ChatModel calls an OpenAI-compatible chat completions endpoint.
type ChatModel struct {
	Model      string
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
}

// Turn implements goagent.Model.
func (m ChatModel) Turn(ctx context.Context, request goagent.TurnRequest) (goagent.TurnResult, error) {
	if m.Model == "" {
		return goagent.TurnResult{}, fmt.Errorf("goagent/openai: model is required")
	}
	if m.APIKey == "" {
		return goagent.TurnResult{}, fmt.Errorf("goagent/openai: API key is required")
	}

	body := chatRequest{
		Model:    m.Model,
		Messages: openAIMessages(request),
		Tools:    openAITools(request.Tools),
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return goagent.TurnResult{}, fmt.Errorf("goagent/openai: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(m.baseURL(), "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return goagent.TurnResult{}, fmt.Errorf("goagent/openai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.APIKey)

	httpResp, err := m.client().Do(httpReq)
	if err != nil {
		return goagent.TurnResult{}, fmt.Errorf("goagent/openai: request failed: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return goagent.TurnResult{}, fmt.Errorf("goagent/openai: read response: %w", err)
	}
	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		return goagent.TurnResult{}, fmt.Errorf("goagent/openai: status %d: %s", httpResp.StatusCode, string(data))
	}

	var response chatResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return goagent.TurnResult{}, fmt.Errorf("goagent/openai: decode response: %w", err)
	}
	if len(response.Choices) == 0 {
		return goagent.TurnResult{}, fmt.Errorf("goagent/openai: response contained no choices")
	}

	choice := response.Choices[0]
	toolCalls := goagentToolCalls(choice.Message.ToolCalls)
	result := goagent.TurnResult{
		Message:   goagent.Message{Role: goagent.RoleAssistant, Content: choice.Message.Content, ToolCalls: toolCalls},
		ToolCalls: toolCalls,
	}
	if len(result.ToolCalls) == 0 && choice.FinishReason == "stop" {
		result.StopReason = goagent.StopComplete
	}
	return result, nil
}

func (m ChatModel) baseURL() string {
	if m.BaseURL != "" {
		return m.BaseURL
	}
	return defaultBaseURL
}

func (m ChatModel) client() *http.Client {
	if m.HTTPClient != nil {
		return m.HTTPClient
	}
	return http.DefaultClient
}

type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Tools    []chatTool    `json:"tools,omitempty"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Parameters  goagent.ToolSchema `json:"parameters,omitempty"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
}

type chatChoice struct {
	Message      chatChoiceMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

type chatChoiceMessage struct {
	Role      string         `json:"role"`
	Content   string         `json:"content"`
	ToolCalls []chatToolCall `json:"tool_calls"`
}

type chatToolCall struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function chatToolCallFunction `json:"function"`
}

type chatToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

func openAIMessages(request goagent.TurnRequest) []chatMessage {
	messages := make([]chatMessage, 0, len(request.Messages)+1)
	if request.Instructions != "" {
		messages = append(messages, chatMessage{Role: string(goagent.RoleSystem), Content: request.Instructions})
	}
	for _, message := range request.Messages {
		messages = append(messages, chatMessage{
			Role:       string(message.Role),
			Content:    message.Content,
			Name:       message.Name,
			ToolCallID: message.ToolCallID,
			ToolCalls:  openAIToolCalls(message.ToolCalls),
		})
	}
	return messages
}

func openAIToolCalls(calls []goagent.ToolCall) []chatToolCall {
	toolCalls := make([]chatToolCall, 0, len(calls))
	for _, call := range calls {
		toolCalls = append(toolCalls, chatToolCall{
			ID:   call.ID,
			Type: "function",
			Function: chatToolCallFunction{
				Name:      call.Name,
				Arguments: string(call.Input),
			},
		})
	}
	return toolCalls
}

func goagentToolCalls(calls []chatToolCall) []goagent.ToolCall {
	toolCalls := make([]goagent.ToolCall, 0, len(calls))
	for _, call := range calls {
		toolCalls = append(toolCalls, goagent.ToolCall{
			ID:    call.ID,
			Name:  call.Function.Name,
			Input: json.RawMessage(call.Function.Arguments),
		})
	}
	return toolCalls
}

func openAITools(specs []goagent.ToolSpec) []chatTool {
	tools := make([]chatTool, 0, len(specs))
	for _, spec := range specs {
		tools = append(tools, chatTool{
			Type: "function",
			Function: chatFunction{
				Name:        spec.Name,
				Description: spec.Description,
				Parameters:  spec.Schema,
			},
		})
	}
	return tools
}

var _ goagent.Model = ChatModel{}
