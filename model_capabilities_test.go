package goagent_test

import (
	"context"
	"testing"

	goagent "github.com/jgabor/go-agent"
)

func TestModelCapabilitiesOfAbsent(t *testing.T) {
	m := goagent.ModelFromSimple(goagent.SimpleModelFunc(func(context.Context, goagent.TurnRequest) (goagent.TurnResult, error) {
		return goagent.TurnResult{}, nil
	}))
	_, ok := goagent.ModelCapabilitiesOf(m)
	if ok {
		t.Fatal("expected ModelFromSimple adapter not to expose ModelCapabilitiesProvider")
	}
}

func TestModelCapabilitiesOfNil(t *testing.T) {
	_, ok := goagent.ModelCapabilitiesOf(nil)
	if ok {
		t.Fatal("expected nil model to report no capabilities")
	}
}

func TestSimpleModelAdapterStreamsWithoutCapabilityProvider(t *testing.T) {
	m := goagent.ModelFromSimple(goagent.SimpleModelFunc(func(context.Context, goagent.TurnRequest) (goagent.TurnResult, error) {
		return goagent.TurnResult{
			Message: goagent.Message{Role: goagent.RoleAssistant, Content: "ok"},
		}, nil
	}))
	if _, ok := goagent.ModelCapabilitiesOf(m); ok {
		t.Fatal("expected adapter without ModelCapabilitiesProvider")
	}
	ctx := context.Background()
	err := m.Stream(ctx, goagent.TurnRequest{}, func(goagent.Event) {})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
}
