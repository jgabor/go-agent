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
