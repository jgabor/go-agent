package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	goagent "github.com/jgabor/go-agent"
)

func TestWorkerProcessesJob(t *testing.T) {
	worker, err := newWorker()
	if err != nil {
		t.Fatal(err)
	}

	result, err := worker.process(context.Background(), job{ID: "job-1", Prompt: "Summarize billing anomalies", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != goagent.StopComplete {
		t.Fatalf("StopReason = %q", result.StopReason)
	}
	if !strings.Contains(result.Text, "billing anomalies") {
		t.Fatalf("Text = %q, want job topic", result.Text)
	}
}

func TestWorkerResumesLatestJobTurn(t *testing.T) {
	worker, err := newWorker()
	if err != nil {
		t.Fatal(err)
	}

	for _, prompt := range []string{"Summarize billing anomalies", "Summarize delayed shipments"} {
		result, err := worker.process(context.Background(), job{ID: "job-1", Prompt: prompt, Timeout: time.Second})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(prompt, "delayed") && !strings.Contains(result.Text, "delayed shipments") {
			t.Fatalf("Text = %q, want latest job prompt", result.Text)
		}
	}
}

func TestWorkerHonorsCanceledContext(t *testing.T) {
	worker, err := newWorker()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := worker.process(ctx, job{ID: "job-1", Prompt: "Summarize billing anomalies", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != goagent.StopCanceled {
		t.Fatalf("StopReason = %q, want %q", result.StopReason, goagent.StopCanceled)
	}
}

func TestWorkerReportsDeadlineFromModel(t *testing.T) {
	runner, err := goagent.NewRunner(goagent.Agent{Model: deadlineModel{}})
	if err != nil {
		t.Fatal(err)
	}
	worker := &worker{runner: runner}

	result, err := worker.process(context.Background(), job{ID: "job-1", Prompt: "slow", Timeout: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	if result.StopReason != goagent.StopModelError && result.StopReason != goagent.StopCanceled {
		t.Fatalf("StopReason = %q, want model error or cancellation", result.StopReason)
	}
}

type deadlineModel struct{}

func (deadlineModel) Turn(ctx context.Context, request goagent.TurnRequest) (goagent.TurnResult, error) {
	<-ctx.Done()
	return goagent.TurnResult{}, ctx.Err()
}

func TestLatestUserInput(t *testing.T) {
	got := latestUserInput([]goagent.Message{
		{Role: goagent.RoleUser, Content: "first"},
		{Role: goagent.RoleAssistant, Content: "assistant"},
		{Role: goagent.RoleUser, Content: "second"},
	})
	if got != "second" {
		t.Fatalf("latestUserInput = %q", got)
	}
	if got := latestUserInput(nil); got != "unknown job" {
		t.Fatalf("latestUserInput(nil) = %q", got)
	}
	if got := latestUserInput([]goagent.Message{{Role: goagent.RoleAssistant, Content: "assistant"}}); got != "assistant" {
		t.Fatalf("latestUserInput(no user) = %q", got)
	}
}

func TestJobModelPropagatesCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&jobModel{}).Turn(ctx, goagent.TurnRequest{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}
