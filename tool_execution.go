package goagent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
)

func (t functionTool) Call(ctx context.Context, call ToolCall) (ToolResult, error) {
	return executeFunctionTool(ctx, t.spec, t.fn, t.inputType, call)
}

func executeFunctionTool(ctx context.Context, spec ToolSpec, fn reflect.Value, inputType reflect.Type, call ToolCall) (ToolResult, error) {
	if call.Name != spec.Name {
		return ToolResult{}, fmt.Errorf("goagent: tool %q cannot execute call for %q", spec.Name, call.Name)
	}
	if spec.Constraints.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, spec.Constraints.Timeout)
		defer cancel()
	}

	input, err := decodeToolInput(spec.Name, inputType, call.Input)
	if err != nil {
		return ToolResult{}, err
	}

	outputs := fn.Call([]reflect.Value{reflect.ValueOf(ctx), input})
	if errValue := outputs[1]; !errValue.IsNil() {
		err := errValue.Interface().(error)
		return ToolResult{}, fmt.Errorf("goagent: tool %q failed: %w", spec.Name, err)
	}
	content := outputs[0].String()
	if spec.Constraints.MaxOutputBytes > 0 && len(content) > spec.Constraints.MaxOutputBytes {
		return ToolResult{}, fmt.Errorf("goagent: tool %q output exceeds max output bytes %d", spec.Name, spec.Constraints.MaxOutputBytes)
	}

	return ToolResult{CallID: call.ID, Name: call.Name, Content: content}, nil
}

func (tool registeredTool) invoke(ctx context.Context, r *runner, state *runState, turnID string, call ToolCall) (ToolResult, error) {
	if st, ok := tool.tool.(StreamingTool); ok {
		emit := newRunnerToolProgressEmitter(state, turnID, tool.spec, call)
		return st.CallStream(ctx, call, emit)
	}
	return tool.tool.Call(ctx, call)
}

func (tool registeredTool) callWithRetry(ctx context.Context, r *runner, state *runState, turnID string, call ToolCall) (ToolResult, StopReason, error) {
	spec := cloneToolSpec(tool.spec)
	return runRetryOperation(ctx, r, retryOperation[ToolResult]{
		state:        state,
		turnID:       turnID,
		toolCallID:   call.ID,
		tool:         spec,
		toolCall:     call,
		target:       RetryTarget{Kind: RetryTargetTool, TurnID: turnID, ToolCallID: call.ID, ToolName: call.Name},
		reason:       RetryReasonToolError,
		disabledStop: StopToolError,
		retryable:    spec.Safety.Retryable,
		blockReason:  RetryReasonToolRetryBlocked,
		blockStop:    StopToolError,
		call: func() (ToolResult, error) {
			return tool.invoke(ctx, r, state, turnID, call)
		},
	})
}

func decodeSingleStringInput(toolName string, raw json.RawMessage) (string, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return "", fmt.Errorf("goagent: tool %q input must be a JSON object with one string field: %w", toolName, err)
	}
	if len(object) != 1 {
		return "", fmt.Errorf("goagent: tool %q input must contain exactly one string field", toolName)
	}

	for _, valueRaw := range object {
		var value string
		if err := json.Unmarshal(valueRaw, &value); err != nil {
			return "", fmt.Errorf("goagent: tool %q input value must be a string: %w", toolName, err)
		}
		return value, nil
	}

	panic("unreachable: map length checked above")
}

func decodeToolInput(toolName string, inputType reflect.Type, raw json.RawMessage) (reflect.Value, error) {
	if inputType == stringType {
		input, err := decodeSingleStringInput(toolName, raw)
		if err != nil {
			return reflect.Value{}, err
		}
		return reflect.ValueOf(input), nil
	}

	input := reflect.New(inputType)
	if err := json.Unmarshal(raw, input.Interface()); err != nil {
		return reflect.Value{}, fmt.Errorf("goagent: tool %q input must match %s: %w", toolName, inputType.Name(), err)
	}
	return input.Elem(), nil
}
