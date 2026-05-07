// Package openai adapts OpenAI-compatible Chat Completions SSE APIs to goagent.Model.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	goagent "github.com/jgabor/go-agent"
)

const defaultBaseURL = "https://api.openai.com/v1"

const (
	providerName = "openai-compatible"
	packageName  = "github.com/jgabor/go-agent/providers/openai"
	maxExcerpt   = 512
)

var excerptRedactions = []*regexp.Regexp{
	regexp.MustCompile(`(?i)"?authorization"?\s*[:=]\s*"?bearer\s+[^\s,"'}]+`),
	regexp.MustCompile(`(?i)"?api[_-]?key"?\s*[:=]\s*"?[^\s,"'}]+`),
	regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^\s/@]+:[^\s/@]+@`),
	regexp.MustCompile(`(?is)"?(prompt|messages|tool[_ -]?args?|arguments)"?\s*[:=]\s*(\[[^\]]*\]|\{[^}]*\}|"[^"]*"|'[^']*'|[^,;\n]+)`),
	regexp.MustCompile(`(?i)"?(env|environment|[A-Z][A-Z0-9_]{2,})"?\s*=\s*"?[^\s,"'}]+`),
}

// ChatModel calls an OpenAI-compatible chat completions endpoint.
type ChatModel struct {
	Model      string
	APIKey     string
	BaseURL    string
	HTTPClient *http.Client
	Options    ChatOptions
}

// ChatOptions carries bounded OpenAI-compatible Chat Completions options.
type ChatOptions struct {
	ReasoningEffort string
	ResponseFormat  ResponseFormat
	// IncludeStreamUsage requests stream_options.include_usage when the streaming adapter is used.
	IncludeStreamUsage bool
}

// ResponseFormat configures OpenAI-compatible response_format without using an untyped pass-through map.
type ResponseFormat struct {
	Type       ResponseFormatType
	JSONSchema *JSONSchemaResponseFormat
}

// ResponseFormatType identifies supported OpenAI-compatible response_format kinds.
type ResponseFormatType string

const (
	ResponseFormatText       ResponseFormatType = "text"
	ResponseFormatJSONObject ResponseFormatType = "json_object"
	ResponseFormatJSONSchema ResponseFormatType = "json_schema"
)

// JSONSchemaResponseFormat describes a bounded json_schema response format.
type JSONSchemaResponseFormat struct {
	Name        string
	Description string
	Schema      goagent.ToolSchema
	Strict      bool
}

// Stream implements goagent.Model.
func (m ChatModel) Stream(ctx context.Context, request goagent.TurnRequest, emit func(goagent.Event)) error {
	if m.Model == "" {
		return fmt.Errorf("goagent/openai: model is required")
	}
	if m.APIKey == "" {
		return fmt.Errorf("goagent/openai: API key is required")
	}

	body := m.chatRequest(request, true)
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("goagent/openai: encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(m.baseURL(), "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("goagent/openai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+m.APIKey)

	httpResp, err := m.client().Do(httpReq)
	if err != nil {
		return fmt.Errorf("goagent/openai: request failed: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	diagnostics := goagent.ProviderDiagnostics{
		Provider:   providerName,
		Package:    packageName,
		RequestID:  responseRequestID(httpResp),
		HTTPStatus: httpResp.StatusCode,
	}
	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		data, readErr := io.ReadAll(httpResp.Body)
		if readErr != nil {
			return fmt.Errorf("goagent/openai: read response: %w", readErr)
		}
		diagnostics.Excerpt = boundedExcerpt(data)
		applyErrorDiagnostics(data, &diagnostics)
		return &goagent.ProviderError{
			Message:     fmt.Sprintf("goagent/openai: status %d", httpResp.StatusCode),
			Diagnostics: diagnostics,
		}
	}
	if !isEventStream(httpResp.Header.Get("Content-Type")) {
		data, readErr := io.ReadAll(httpResp.Body)
		if readErr != nil {
			return fmt.Errorf("goagent/openai: read non-SSE response: %w", readErr)
		}
		diagnostics.Excerpt = boundedExcerpt(data)
		return &goagent.ProviderError{
			Message:     "goagent/openai: expected SSE stream response",
			Diagnostics: diagnostics,
		}
	}

	return streamChatCompletions(ctx, httpResp.Body, diagnostics, m.Model, emit)
}

func (m ChatModel) chatRequest(request goagent.TurnRequest, stream bool) chatRequest {
	body := chatRequest{
		Model:               m.Model,
		Messages:            openAIMessages(request),
		Tools:               openAITools(request.Tools),
		MaxCompletionTokens: request.Options.MaxOutputTokens,
		Temperature:         request.Options.Temperature,
		Stop:                append([]string(nil), request.Options.StopSequences...),
		ReasoningEffort:     m.Options.ReasoningEffort,
		ResponseFormat:      openAIResponseFormat(m.Options.ResponseFormat),
		Stream:              stream,
	}
	if stream && m.Options.IncludeStreamUsage {
		body.StreamOptions = &chatStreamOptions{IncludeUsage: true}
	}
	return body
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
	Model               string              `json:"model"`
	Messages            []chatMessage       `json:"messages"`
	Tools               []chatTool          `json:"tools,omitempty"`
	MaxCompletionTokens int                 `json:"max_completion_tokens,omitempty"`
	Temperature         *float64            `json:"temperature,omitempty"`
	Stop                []string            `json:"stop,omitempty"`
	ReasoningEffort     string              `json:"reasoning_effort,omitempty"`
	ResponseFormat      *chatResponseFormat `json:"response_format,omitempty"`
	Stream              bool                `json:"stream,omitempty"`
	StreamOptions       *chatStreamOptions  `json:"stream_options,omitempty"`
}

type chatStreamOptions struct {
	IncludeUsage bool `json:"include_usage,omitempty"`
}

type chatResponseFormat struct {
	Type       ResponseFormatType        `json:"type"`
	JSONSchema *chatResponseFormatSchema `json:"json_schema,omitempty"`
}

type chatResponseFormatSchema struct {
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Schema      goagent.ToolSchema `json:"schema,omitempty"`
	Strict      bool               `json:"strict,omitempty"`
}

type chatErrorResponse struct {
	Error chatError `json:"error"`
}

type chatError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    string `json:"code"`
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

type chatStreamChunk struct {
	ID      string               `json:"id"`
	Model   string               `json:"model"`
	Choices []chatStreamChoice   `json:"choices"`
	Usage   *chatCompletionUsage `json:"usage"`
}

type chatStreamChoice struct {
	Index        int             `json:"index"`
	Delta        chatStreamDelta `json:"delta"`
	FinishReason string          `json:"finish_reason"`
}

type chatStreamDelta struct {
	Role      string               `json:"role"`
	Content   *string              `json:"content"`
	ToolCalls []chatStreamToolCall `json:"tool_calls"`
}

type chatStreamToolCall struct {
	Index    int                    `json:"index"`
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function chatStreamToolFunction `json:"function"`
}

type chatStreamToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatCompletionUsage struct {
	PromptTokens            int                    `json:"prompt_tokens"`
	CompletionTokens        int                    `json:"completion_tokens"`
	TotalTokens             int                    `json:"total_tokens"`
	PromptTokensDetails     chatPromptTokenDetails `json:"prompt_tokens_details"`
	CompletionTokensDetails chatCompletionDetails  `json:"completion_tokens_details"`
}

type chatPromptTokenDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

type chatCompletionDetails struct {
	AcceptedPredictionTokens int `json:"accepted_prediction_tokens"`
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

type streamState struct {
	emit        func(goagent.Event)
	diagnostics goagent.ProviderDiagnostics
	model       string
	messageID   string
	started     bool
	finalized   bool
	textBlockID string
	text        strings.Builder
	toolBlocks  map[int]*streamToolBlock
	toolOrder   []int
	usage       goagent.Usage
}

type streamToolBlock struct {
	blockID string
	callID  string
	name    strings.Builder
	args    strings.Builder
}

func streamChatCompletions(ctx context.Context, body io.Reader, diagnostics goagent.ProviderDiagnostics, model string, emit func(goagent.Event)) error {
	if emit == nil {
		emit = func(goagent.Event) {}
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	state := streamState{
		emit:        emit,
		diagnostics: diagnostics,
		model:       model,
		messageID:   "message-1",
		toolBlocks:  map[int]*streamToolBlock{},
	}
	state.ensureStarted()

	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var record []string
	for scanner.Scan() {
		line := scanner.Text()
		if err := context.Cause(ctx); err != nil {
			return state.fail(err)
		}
		if line == "" {
			if err := state.processRecord(record); err != nil {
				return state.fail(err)
			}
			if err := context.Cause(ctx); err != nil {
				return state.fail(err)
			}
			record = nil
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		if data, ok := strings.CutPrefix(line, "data:"); ok {
			record = append(record, strings.TrimSpace(data))
		}
	}
	if err := scanner.Err(); err != nil {
		if ctxErr := context.Cause(ctx); ctxErr != nil {
			return state.fail(ctxErr)
		}
		return state.fail(fmt.Errorf("goagent/openai: read SSE stream: %w", err))
	}
	if err := context.Cause(ctx); err != nil {
		return state.fail(err)
	}
	if err := state.processRecord(record); err != nil {
		return state.fail(err)
	}
	if !state.finalized {
		return state.fail(errors.New("goagent/openai: stream ended before finish_reason"))
	}
	state.succeed()
	return nil
}

func (s *streamState) processRecord(lines []string) error {
	if len(lines) == 0 {
		return nil
	}
	data := strings.Join(lines, "\n")
	if data == "[DONE]" {
		return nil
	}
	var chunk chatStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return fmt.Errorf("goagent/openai: decode SSE chunk: %w", err)
	}
	if chunk.ID != "" && s.diagnostics.RequestID == "" {
		s.diagnostics.RequestID = chunk.ID
	}
	if chunk.Model != "" {
		s.model = chunk.Model
	}
	if chunk.Usage != nil {
		s.usage = usageFromChunk(*chunk.Usage, s.diagnostics.RequestID, s.model)
	}
	if len(chunk.Choices) > 1 {
		return errors.New("goagent/openai: stream chunk contained multiple choices")
	}
	for _, choice := range chunk.Choices {
		if choice.Index != 0 {
			return fmt.Errorf("goagent/openai: unsupported stream choice index %d", choice.Index)
		}
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			s.textDelta(*choice.Delta.Content)
		}
		for _, toolCall := range choice.Delta.ToolCalls {
			if err := s.toolCallDelta(toolCall); err != nil {
				return err
			}
		}
		if choice.FinishReason != "" {
			s.diagnostics.RawStopReason = choice.FinishReason
			if err := s.finalize(); err != nil {
				return err
			}
		}
	}
	if chunk.Usage != nil && s.finalized {
		s.emitUsage()
	}
	return nil
}

func (s *streamState) ensureStarted() {
	if s.started || s.emit == nil {
		return
	}
	s.emit(goagent.Event{Kind: goagent.EventResponseStart, MessageID: s.messageID, Diagnostics: s.diagnostics})
	s.started = true
}

func (s *streamState) textDelta(text string) {
	if s.textBlockID == "" {
		s.textBlockID = "block-text-1"
		s.emit(goagent.Event{Kind: goagent.EventContentBlockStart, MessageID: s.messageID, BlockID: s.textBlockID, BlockKind: goagent.BlockText, Diagnostics: s.diagnostics})
	}
	s.text.WriteString(text)
	s.emit(goagent.Event{Kind: goagent.EventTextDelta, BlockID: s.textBlockID, Text: text, Diagnostics: s.diagnostics})
}

func (s *streamState) toolCallDelta(delta chatStreamToolCall) error {
	if delta.Type != "" && delta.Type != "function" {
		return fmt.Errorf("goagent/openai: unsupported tool call type %q", delta.Type)
	}
	block := s.toolBlocks[delta.Index]
	if block == nil {
		callID := delta.ID
		if callID == "" {
			callID = fmt.Sprintf("tool-call-%d", delta.Index)
		}
		block = &streamToolBlock{blockID: fmt.Sprintf("block-tool-%d", delta.Index+1), callID: callID}
		s.toolBlocks[delta.Index] = block
		s.toolOrder = append(s.toolOrder, delta.Index)
		s.emit(goagent.Event{Kind: goagent.EventContentBlockStart, MessageID: s.messageID, BlockID: block.blockID, BlockKind: goagent.BlockToolCall, ToolCallID: block.callID, Diagnostics: s.diagnostics})
	} else if delta.ID != "" && delta.ID != block.callID {
		return fmt.Errorf("goagent/openai: tool call ID changed for index %d", delta.Index)
	}
	if delta.ID != "" {
		block.callID = delta.ID
	}
	block.name.WriteString(delta.Function.Name)
	block.args.WriteString(delta.Function.Arguments)
	s.emit(goagent.Event{
		Kind:       goagent.EventToolCallDelta,
		BlockID:    block.blockID,
		ToolCallID: block.callID,
		ToolCallDelta: goagent.ToolCallDelta{
			Index:          delta.Index,
			NameDelta:      delta.Function.Name,
			ArgumentsDelta: delta.Function.Arguments,
		},
		Diagnostics: s.diagnostics,
	})
	return nil
}

func (s *streamState) finalize() error {
	if s.finalized {
		return errors.New("goagent/openai: duplicate stream finish_reason")
	}
	message := goagent.Message{Role: goagent.RoleAssistant}
	if s.textBlockID != "" {
		text := s.text.String()
		message.Content = text
		message.Blocks = append(message.Blocks, goagent.Block{ID: s.textBlockID, Kind: goagent.BlockText, Text: text})
		s.emit(goagent.Event{Kind: goagent.EventContentBlockEnd, BlockID: s.textBlockID, BlockKind: goagent.BlockText, Text: text, Diagnostics: s.diagnostics})
	}
	for _, index := range s.toolOrder {
		block := s.toolBlocks[index]
		call := goagent.ToolCall{ID: block.callID, Name: block.name.String(), Input: json.RawMessage(block.args.String())}
		if call.Name == "" {
			return fmt.Errorf("goagent/openai: tool call %d missing function name", index)
		}
		if !json.Valid(call.Input) {
			return fmt.Errorf("goagent/openai: tool call %d arguments are not valid JSON", index)
		}
		message.Blocks = append(message.Blocks, goagent.Block{ID: block.blockID, Kind: goagent.BlockToolCall, ToolCall: call})
		message.ToolCalls = append(message.ToolCalls, call)
		s.emit(goagent.Event{Kind: goagent.EventContentBlockEnd, BlockID: block.blockID, BlockKind: goagent.BlockToolCall, ToolCallID: call.ID, ToolCall: call, Diagnostics: s.diagnostics})
	}
	s.emit(goagent.Event{Kind: goagent.EventMessageFinal, MessageID: s.messageID, Message: message, Diagnostics: s.diagnostics})
	for _, call := range message.ToolCalls {
		s.emit(goagent.Event{Kind: goagent.EventToolCallReady, ToolCallID: call.ID, ToolCall: call, Diagnostics: s.diagnostics})
	}
	s.finalized = true
	return nil
}

func (s *streamState) emitUsage() {
	if s.usage == (goagent.Usage{}) || s.emit == nil {
		return
	}
	s.emit(goagent.Event{Kind: goagent.EventUsage, Usage: s.usage, Diagnostics: s.diagnostics})
	s.usage = goagent.Usage{}
}

func (s *streamState) succeed() {
	if len(s.toolOrder) > 0 || s.emit == nil {
		return
	}
	s.emit(goagent.Event{Kind: goagent.EventStop, StopReason: goagent.StopComplete, Diagnostics: s.diagnostics})
}

func (s *streamState) fail(err error) error {
	if s.emit != nil {
		s.emit(goagent.Event{Kind: goagent.EventError, Err: err, Diagnostics: s.diagnostics})
		if s.usage != (goagent.Usage{}) {
			s.emit(goagent.Event{Kind: goagent.EventUsage, Usage: s.usage, Diagnostics: s.diagnostics})
		}
		s.emit(goagent.Event{Kind: goagent.EventStop, StopReason: failureStopReason(err), Diagnostics: s.diagnostics})
	}
	return err
}

func failureStopReason(err error) goagent.StopReason {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return goagent.StopCanceled
	}
	return goagent.StopModelError
}

func usageFromChunk(usage chatCompletionUsage, requestID string, model string) goagent.Usage {
	return goagent.Usage{
		InputTokens:       usage.PromptTokens,
		OutputTokens:      usage.CompletionTokens,
		TotalTokens:       usage.TotalTokens,
		CachedInputTokens: usage.PromptTokensDetails.CachedTokens,
		CacheWriteTokens:  usage.CompletionTokensDetails.AcceptedPredictionTokens,
		RequestID:         requestID,
		Provider:          providerName,
		Model:             model,
	}
}

func isEventStream(contentType string) bool {
	mediaType, _, _ := strings.Cut(contentType, ";")
	return strings.EqualFold(strings.TrimSpace(mediaType), "text/event-stream")
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

func openAIResponseFormat(format ResponseFormat) *chatResponseFormat {
	if format.Type == "" {
		return nil
	}
	responseFormat := &chatResponseFormat{Type: format.Type}
	if format.Type == ResponseFormatJSONSchema && format.JSONSchema != nil {
		responseFormat.JSONSchema = &chatResponseFormatSchema{
			Name:        format.JSONSchema.Name,
			Description: format.JSONSchema.Description,
			Schema:      format.JSONSchema.Schema,
			Strict:      format.JSONSchema.Strict,
		}
	}
	return responseFormat
}

func responseRequestID(response *http.Response) string {
	for _, header := range []string{"X-Request-Id", "X-Request-ID", "OpenAI-Request-ID"} {
		if value := response.Header.Get(header); value != "" {
			return value
		}
	}
	return ""
}

func applyErrorDiagnostics(data []byte, diagnostics *goagent.ProviderDiagnostics) {
	var response chatErrorResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return
	}
	diagnostics.ErrorType = response.Error.Type
	diagnostics.ErrorCode = response.Error.Code
	if response.Error.Message != "" {
		diagnostics.Excerpt = boundedExcerpt([]byte(response.Error.Message))
	}
}

func boundedExcerpt(data []byte) string {
	excerpt := sanitizeExcerpt(strings.TrimSpace(string(data)))
	if len(excerpt) <= maxExcerpt {
		return excerpt
	}
	return excerpt[:maxExcerpt]
}

func sanitizeExcerpt(excerpt string) string {
	for _, redaction := range excerptRedactions {
		excerpt = redaction.ReplaceAllStringFunc(excerpt, func(match string) string {
			if strings.Contains(match, "://") && strings.Contains(match, "@") {
				return redaction.ReplaceAllString(match, `$1[redacted]@`)
			}
			return "[redacted]"
		})
	}
	return excerpt
}

var _ goagent.Model = ChatModel{}
