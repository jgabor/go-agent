package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	goagent "github.com/jgabor/go-agent"
)

func main() {
	worker, err := newWorker()
	if err != nil {
		log.Fatal(err)
	}

	ctx := context.Background()
	jobs := []job{
		{ID: "job-1", Prompt: "Summarize billing anomalies", Timeout: time.Second},
		{ID: "job-2", Prompt: "Summarize delayed shipments", Timeout: time.Second},
	}

	for _, job := range jobs {
		result, err := worker.process(ctx, job)
		if err != nil {
			log.Printf("%s failed: %v", job.ID, err)
			continue
		}
		fmt.Printf("%s: %s\n", job.ID, result.Text)
	}
}

type job struct {
	ID      string
	Prompt  string
	Timeout time.Duration
}

type worker struct {
	runner goagent.Runner
}

func newWorker() (*worker, error) {
	lookup, err := goagent.NewTool("lookup", "Lookup operational facts for a job.", func(ctx context.Context, topic string) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		return "facts for " + topic, nil
	})
	if err != nil {
		return nil, err
	}

	runner, err := goagent.New(
		goagent.WithInstructions("Process background jobs with concise operational summaries."),
		goagent.WithModel(goagent.ModelFromSimple(&jobModel{})),
		goagent.WithTools(lookup),
		goagent.WithSessionStore(goagent.NewMemorySessionStore()),
		goagent.WithEventSinks(goagent.EventSinkFunc(func(ctx context.Context, event goagent.Event) {
			if event.Kind == goagent.EventStop {
				log.Printf("run_id=%s stop=%s", event.RunID, event.StopReason)
			}
		})),
	)
	if err != nil {
		return nil, err
	}

	return &worker{runner: runner}, nil
}

func (w *worker) process(ctx context.Context, job job) (goagent.RunResult, error) {
	if job.Timeout <= 0 {
		job.Timeout = 5 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, job.Timeout)
	defer cancel()

	return w.runner.Run(ctx, goagent.RunRequest{
		Input:     job.Prompt,
		SessionID: job.ID,
		MaxSteps:  4,
	})
}

type jobModel struct{}

func (m *jobModel) Turn(ctx context.Context, request goagent.TurnRequest) (goagent.TurnResult, error) {
	if err := ctx.Err(); err != nil {
		return goagent.TurnResult{}, err
	}
	latestUser := latestUserIndex(request.Messages)
	for _, message := range request.Messages[latestUser+1:] {
		if message.Role == goagent.RoleTool && message.ToolCallID == "call-lookup-1" {
			return goagent.TurnResult{
				Message:    goagent.Message{Role: goagent.RoleAssistant, Content: "Processed " + message.Content + "."},
				StopReason: goagent.StopComplete,
			}, nil
		}
	}

	topic := latestUserInput(request.Messages)
	input, err := json.Marshal(map[string]string{"topic": topic})
	if err != nil {
		return goagent.TurnResult{}, err
	}
	return goagent.TurnResult{ToolCalls: []goagent.ToolCall{{
		ID:    "call-lookup-1",
		Name:  "lookup",
		Input: input,
	}}}, nil
}

func latestUserInput(messages []goagent.Message) string {
	if len(messages) == 0 {
		return "unknown job"
	}
	return messages[latestUserIndex(messages)].Content
}

func latestUserIndex(messages []goagent.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == goagent.RoleUser {
			return i
		}
	}
	return len(messages) - 1
}
