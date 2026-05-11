package goagent

import (
	"context"
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

	spec := ToolSpec{Name: name, Description: description, Schema: cloneToolSchema(schema), Safety: safety, Constraints: constraints}
	return functionTool{spec: spec, fn: toolFn.fn, inputType: toolFn.inputType}, nil
}

type functionTool struct {
	spec      ToolSpec
	fn        reflect.Value
	inputType reflect.Type
}

func (t functionTool) Name() string { return t.spec.Name }

func (t functionTool) Description() string { return t.spec.Description }

func (t functionTool) Schema() ToolSchema {
	return cloneToolSchema(t.spec.Schema)
}

func (t functionTool) Metadata() ToolMetadata {
	return ToolMetadata{Safety: t.spec.Safety, Constraints: t.spec.Constraints}
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
	if definition.Constraints.MaxProgressEvents < 0 {
		return fmt.Errorf("goagent: invalid tool definition %q: max progress events cannot be negative", definition.Name)
	}
	if definition.Constraints.MaxProgressBytes < 0 {
		return fmt.Errorf("goagent: invalid tool definition %q: max progress bytes cannot be negative", definition.Name)
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
	if schema == nil {
		return nil
	}
	clone := make(ToolSchema, len(schema))
	for key, value := range schema {
		clone[key] = cloneToolSchemaValue(value)
	}
	return clone
}

func cloneToolSchemaValue(value any) any {
	switch typed := value.(type) {
	case ToolSchema:
		return cloneToolSchema(typed)
	case map[string]any:
		clone := make(map[string]any, len(typed))
		for key, value := range typed {
			clone[key] = cloneToolSchemaValue(value)
		}
		return clone
	case []any:
		clone := make([]any, len(typed))
		for i, value := range typed {
			clone[i] = cloneToolSchemaValue(value)
		}
		return clone
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}
