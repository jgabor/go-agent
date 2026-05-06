package goagent

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"
)

var (
	contextType = reflect.TypeOf((*context.Context)(nil)).Elem()
	errorType   = reflect.TypeOf((*error)(nil)).Elem()
	stringType  = reflect.TypeOf("")
)

// NewTool adapts a supported Go function into a Tool.
//
// Supported function shapes are func(context.Context, string) (string, error)
// and func(context.Context, T) (string, error), where T is a struct input.
func NewTool(name, description string, fn any) (Tool, error) {
	return newTool(name, description, nil, ToolSafety{}, ToolConstraints{}, fn)
}

// NewToolWithSchema adapts a supported Go function into a Tool with explicit
// schema metadata supplied by the host application.
func NewToolWithSchema(name, description string, schema ToolSchema, fn any) (Tool, error) {
	if schema == nil {
		return nil, fmt.Errorf("goagent: tool %q explicit schema cannot be nil", name)
	}
	return newTool(name, description, schema, ToolSafety{}, ToolConstraints{}, fn)
}

// NewToolFromDefinition adapts an advanced ToolDefinition into a runtime Tool.
func NewToolFromDefinition(definition ToolDefinition) (Tool, error) {
	if err := validateToolDefinition(definition); err != nil {
		return nil, err
	}
	return newTool(definition.Name, definition.Description, definition.Schema, definition.Safety, definition.Constraints, definition.Function)
}

func newTool(name, description string, explicitSchema ToolSchema, safety ToolSafety, constraints ToolConstraints, fn any) (Tool, error) {
	if err := validateToolName(name); err != nil {
		return nil, err
	}

	toolFn, err := parseToolFunc(name, fn)
	if err != nil {
		return nil, err
	}

	schema := explicitSchema
	if schema == nil {
		schema, err = schemaForInputType(name, toolFn.inputType)
		if err != nil {
			return nil, err
		}
	}

	return functionTool{name: name, description: description, fn: toolFn.fn, inputType: toolFn.inputType, schema: cloneToolSchema(schema), safety: safety, constraints: constraints}, nil
}

type functionTool struct {
	name        string
	description string
	fn          reflect.Value
	inputType   reflect.Type
	schema      ToolSchema
	safety      ToolSafety
	constraints ToolConstraints
}

func (t functionTool) Name() string { return t.name }

func (t functionTool) Description() string { return t.description }

func (t functionTool) Schema() ToolSchema {
	return cloneToolSchema(t.schema)
}

func (t functionTool) Metadata() ToolMetadata {
	return ToolMetadata{Safety: t.safety, Constraints: t.constraints}
}

func (t functionTool) Call(ctx context.Context, call ToolCall) (ToolResult, error) {
	if call.Name != t.name {
		return ToolResult{}, fmt.Errorf("goagent: tool %q cannot execute call for %q", t.name, call.Name)
	}
	if t.constraints.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.constraints.Timeout)
		defer cancel()
	}

	input, err := decodeToolInput(t.name, t.inputType, call.Input)
	if err != nil {
		return ToolResult{}, err
	}

	outputs := t.fn.Call([]reflect.Value{reflect.ValueOf(ctx), input})
	if errValue := outputs[1]; !errValue.IsNil() {
		err := errValue.Interface().(error)
		return ToolResult{}, fmt.Errorf("goagent: tool %q failed: %w", t.name, err)
	}
	content := outputs[0].String()
	if t.constraints.MaxOutputBytes > 0 && len(content) > t.constraints.MaxOutputBytes {
		return ToolResult{}, fmt.Errorf("goagent: tool %q output exceeds max output bytes %d", t.name, t.constraints.MaxOutputBytes)
	}

	return ToolResult{CallID: call.ID, Name: call.Name, Content: content}, nil
}

type toolMetadataProvider interface {
	Metadata() ToolMetadata
}

func validateToolDefinition(definition ToolDefinition) error {
	if err := validateToolName(definition.Name); err != nil {
		return fmt.Errorf("goagent: invalid tool definition: %w", err)
	}
	if strings.TrimSpace(definition.Description) == "" {
		return fmt.Errorf("goagent: invalid tool definition %q: description cannot be empty", definition.Name)
	}
	if definition.Schema == nil {
		return fmt.Errorf("goagent: invalid tool definition %q: schema cannot be nil", definition.Name)
	}
	if definition.Constraints.Timeout < 0*time.Second {
		return fmt.Errorf("goagent: invalid tool definition %q: timeout cannot be negative", definition.Name)
	}
	if definition.Constraints.MaxOutputBytes < 0 {
		return fmt.Errorf("goagent: invalid tool definition %q: max output bytes cannot be negative", definition.Name)
	}
	return nil
}

type toolFunc struct {
	fn        reflect.Value
	inputType reflect.Type
}

func parseToolFunc(name string, fn any) (toolFunc, error) {
	if fn == nil {
		return toolFunc{}, unsupportedToolFuncError(name)
	}

	value := reflect.ValueOf(fn)
	typ := value.Type()
	if typ.Kind() != reflect.Func || typ.NumIn() != 2 || typ.NumOut() != 2 {
		return toolFunc{}, unsupportedToolFuncError(name)
	}
	if !typ.In(0).Implements(contextType) || typ.Out(0) != stringType || !typ.Out(1).Implements(errorType) {
		return toolFunc{}, unsupportedToolFuncError(name)
	}

	inputType := typ.In(1)
	if inputType != stringType && inputType.Kind() != reflect.Struct {
		return toolFunc{}, unsupportedToolFuncError(name)
	}

	return toolFunc{fn: value, inputType: inputType}, nil
}

func unsupportedToolFuncError(name string) error {
	return fmt.Errorf("goagent: tool %q function must have signature func(context.Context, string|struct) (string, error)", name)
}

func validateToolName(name string) error {
	if name == "" {
		return fmt.Errorf("goagent: tool name cannot be empty")
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return fmt.Errorf("goagent: tool name %q contains invalid character %q", name, r)
	}
	return nil
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

func schemaForInputType(toolName string, inputType reflect.Type) (ToolSchema, error) {
	if inputType == stringType {
		return ToolSchema{
			"type": "object",
			"additionalProperties": map[string]any{
				"type": "string",
			},
			"minProperties": 1,
			"maxProperties": 1,
		}, nil
	}

	return schemaForStruct(toolName, inputType)
}

func schemaForStruct(toolName string, inputType reflect.Type) (ToolSchema, error) {
	properties := map[string]any{}
	var required []string

	for i := 0; i < inputType.NumField(); i++ {
		field := inputType.Field(i)
		if field.PkgPath != "" {
			continue
		}

		name, omitEmpty, ok := jsonFieldName(field)
		if !ok {
			continue
		}

		fieldSchema, err := schemaForField(toolName, field.Type)
		if err != nil {
			return nil, fmt.Errorf("goagent: tool %q field %s: %w", toolName, field.Name, err)
		}
		if description := field.Tag.Get("description"); description != "" {
			fieldSchema["description"] = description
		}

		properties[name] = fieldSchema
		if !omitEmpty {
			required = append(required, name)
		}
	}

	schema := ToolSchema{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema, nil
}

func jsonFieldName(field reflect.StructField) (string, bool, bool) {
	tag := field.Tag.Get("json")
	if tag == "-" {
		return "", false, false
	}

	parts := strings.Split(tag, ",")
	name := parts[0]
	if name == "" {
		name = field.Name
	}

	omitEmpty := false
	for _, option := range parts[1:] {
		if option == "omitempty" {
			omitEmpty = true
			break
		}
	}

	return name, omitEmpty, true
}

func schemaForField(toolName string, typ reflect.Type) (map[string]any, error) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	switch typ.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil
	case reflect.Slice, reflect.Array:
		items, err := schemaForField(toolName, typ.Elem())
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": items}, nil
	case reflect.Map:
		if typ.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("map schema only supports string keys")
		}
		return map[string]any{"type": "object"}, nil
	case reflect.Struct:
		schema, err := schemaForStruct(toolName, typ)
		if err != nil {
			return nil, err
		}
		return map[string]any(schema), nil
	default:
		return nil, fmt.Errorf("unsupported schema type %s", typ)
	}
}

func cloneToolSchema(schema ToolSchema) ToolSchema {
	clone := make(ToolSchema, len(schema))
	for key, value := range schema {
		clone[key] = value
	}
	return clone
}
