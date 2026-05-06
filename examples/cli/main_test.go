package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	goagent "github.com/jgabor/go-agent"
)

func TestRunCLIPrintsFinalText(t *testing.T) {
	var out bytes.Buffer
	if err := runCLI(context.Background(), []string{"-input", "Weather in Berlin?"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Berlin") {
		t.Fatalf("output = %q, want Berlin forecast", out.String())
	}
}

func TestRunCLIStreamsEvents(t *testing.T) {
	var out bytes.Buffer
	if err := runCLI(context.Background(), []string{"-input", "Weather in Tokyo?", "-stream"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"kind":"tool_call"`) {
		t.Fatalf("stream output missing tool call: %s", out.String())
	}
	if !strings.Contains(out.String(), `"kind":"stop"`) {
		t.Fatalf("stream output missing stop: %s", out.String())
	}
}

func TestRunCLIRejectsInvalidFlag(t *testing.T) {
	var out bytes.Buffer
	if err := runCLI(context.Background(), []string{"-unknown"}, &out); err == nil {
		t.Fatal("runCLI succeeded with unknown flag")
	}
}

func TestCityFromInput(t *testing.T) {
	got := cityFromInput([]goagent.Message{{Role: goagent.RoleUser, Content: "What about Tokyo?"}})
	if got != "Tokyo" {
		t.Fatalf("cityFromInput = %q", got)
	}
	if got := cityFromInput(nil); got != "Austin" {
		t.Fatalf("cityFromInput(nil) = %q", got)
	}
}
