package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	goagent "github.com/jgabor/go-agent"
)

func main() {
	if err := runCLI(context.Background(), os.Args[1:], os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func runCLI(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("go-agent-example", flag.ContinueOnError)
	flags.SetOutput(out)
	input := flags.String("input", "Should I bring a jacket to Austin tonight?", "prompt to run")
	sessionID := flags.String("session", "cli-demo", "session id to resume")
	stream := flags.Bool("stream", false, "print structured event stream instead of final text")
	timeout := flags.Duration("timeout", 2*time.Second, "run timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}

	runner, err := newRunner()
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	request := goagent.RunRequest{Input: *input, SessionID: *sessionID, MaxSteps: 4}
	if *stream {
		return streamEvents(ctx, out, runner, request)
	}

	result, err := runner.Run(ctx, request)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, result.Text)
	return err
}

func streamEvents(ctx context.Context, out io.Writer, runner goagent.Runner, request goagent.RunRequest) error {
	events, err := runner.Stream(ctx, request)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	for event := range events {
		if err := encoder.Encode(eventLine{Sequence: event.Sequence, Kind: event.Kind, Text: event.Text, StopReason: event.StopReason}); err != nil {
			return err
		}
	}
	return nil
}

type eventLine struct {
	Sequence   int64              `json:"sequence"`
	Kind       goagent.EventKind  `json:"kind"`
	Text       string             `json:"text,omitempty"`
	StopReason goagent.StopReason `json:"stop_reason,omitempty"`
}

func newRunner() (goagent.Runner, error) {
	weather, err := goagent.NewTool("weather", "Get weather for a city.", func(ctx context.Context, city string) (string, error) {
		return "72F and clear in " + city, nil
	})
	if err != nil {
		return nil, err
	}

	return goagent.New(
		goagent.WithInstructions("Answer as a concise command-line weather assistant."),
		goagent.WithModel(goagent.ModelFromSimple(&cliModel{})),
		goagent.WithTools(weather),
		goagent.WithSessionStore(goagent.NewMemorySessionStore()),
	)
}

type cliModel struct{}

func (m *cliModel) Turn(ctx context.Context, request goagent.TurnRequest) (goagent.TurnResult, error) {
	if err := ctx.Err(); err != nil {
		return goagent.TurnResult{}, err
	}
	latestUser := latestUserIndex(request.Messages)
	for _, message := range request.Messages[latestUser+1:] {
		if message.Role == goagent.RoleTool && message.ToolCallID == "call-weather-1" {
			return goagent.TurnResult{
				Message:    goagent.Message{Role: goagent.RoleAssistant, Content: "Forecast: " + message.Content + "."},
				StopReason: goagent.StopComplete,
			}, nil
		}
	}

	input, err := json.Marshal(map[string]string{"city": cityFromInput(request.Messages)})
	if err != nil {
		return goagent.TurnResult{}, err
	}
	return goagent.TurnResult{ToolCalls: []goagent.ToolCall{{ID: "call-weather-1", Name: "weather", Input: input}}}, nil
}

func cityFromInput(messages []goagent.Message) string {
	if len(messages) == 0 {
		return "Austin"
	}
	input := latestUserInput(messages)
	for _, city := range []string{"Austin", "Berlin", "Tokyo"} {
		if strings.Contains(strings.ToLower(input), strings.ToLower(city)) {
			return city
		}
	}
	return "Austin"
}

func latestUserInput(messages []goagent.Message) string {
	if len(messages) == 0 {
		return ""
	}
	return messages[latestUserIndex(messages)].Content
}

func latestUserIndex(messages []goagent.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == goagent.RoleUser {
			return i
		}
	}
	if len(messages) == 0 {
		return -1
	}
	return len(messages) - 1
}
