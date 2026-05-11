package goagent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
)

func (t functionTool) Call(ctx context.Context, call ToolCall) (ToolResult, error) {
	return executeFunctionTool(ctx, t.spec, t.fn, t.inputType, t.resultType, call)
}

func executeFunctionTool(ctx context.Context, spec ToolSpec, fn reflect.Value, inputType reflect.Type, resultType reflect.Type, call ToolCall) (ToolResult, error) {
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

	result, err := prepareFunctionToolResult(spec, call, resultType, outputs[0])
	if err != nil {
		return ToolResult{}, err
	}
	if spec.Constraints.MaxOutputBytes > 0 && functionToolOutputBytes(result) > spec.Constraints.MaxOutputBytes {
		return ToolResult{}, fmt.Errorf("goagent: tool %q output exceeds max output bytes %d", spec.Name, spec.Constraints.MaxOutputBytes)
	}

	return result, nil
}

func prepareFunctionToolResult(spec ToolSpec, call ToolCall, resultType reflect.Type, output reflect.Value) (ToolResult, error) {
	result, err := functionToolResultFromValue(resultType, output)
	if err != nil {
		return ToolResult{}, fmt.Errorf("goagent: tool %q result invalid: %w", spec.Name, err)
	}
	if result.CallID == "" {
		result.CallID = call.ID
	}
	if result.Name == "" {
		result.Name = call.Name
	}
	result, err = normalizeFunctionToolResult(result)
	if err != nil {
		return ToolResult{}, fmt.Errorf("goagent: tool %q result invalid: %w", spec.Name, err)
	}
	if err := ValidateToolResult(result); err != nil {
		return ToolResult{}, fmt.Errorf("goagent: tool %q result invalid: %w", spec.Name, err)
	}
	return result, nil
}

func functionToolResultFromValue(resultType reflect.Type, output reflect.Value) (ToolResult, error) {
	if resultType == stringType {
		return ToolResult{Content: output.String()}, nil
	}
	if resultType == toolResultType {
		return output.Interface().(ToolResult), nil
	}
	if resultType.Kind() == reflect.Pointer && resultType.Elem() == toolResultType {
		if output.IsNil() {
			return ToolResult{}, fmt.Errorf("*ToolResult cannot be nil")
		}
		result := output.Interface().(*ToolResult)
		return *result, nil
	}
	value, err := normalizeJSONValue(output.Interface())
	if err != nil {
		return ToolResult{}, fmt.Errorf("JSON: %w", err)
	}
	return ToolResult{JSON: value}, nil
}

func normalizeFunctionToolResult(result ToolResult) (ToolResult, error) {
	if result.JSON != nil {
		value, err := normalizeJSONValue(result.JSON)
		if err != nil {
			return ToolResult{}, fmt.Errorf("ToolResult.JSON: %w", err)
		}
		result.JSON = value
	}
	if result.Opaque != nil {
		value, err := normalizeJSONValue(result.Opaque)
		if err != nil {
			return ToolResult{}, fmt.Errorf("ToolResult.Opaque: %w", err)
		}
		if value == nil {
			result.Opaque = nil
		} else {
			opaque, ok := value.(map[string]any)
			if !ok {
				return ToolResult{}, fmt.Errorf("ToolResult.Opaque: normalized to %T, want map[string]any", value)
			}
			result.Opaque = opaque
		}
	}
	return result, nil
}

func normalizeJSONValue(value any) (any, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func functionToolOutputBytes(result ToolResult) int {
	size := len(result.Content)
	if result.JSON != nil {
		data, err := json.Marshal(result.JSON)
		if err != nil {
			return size
		}
		size += len(data)
	}
	return size
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
