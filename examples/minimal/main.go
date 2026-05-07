package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	goagent "github.com/jgabor/go-agent"
)

func main() {
	ctx := context.Background()

	weather, err := goagent.NewTool("weather", "Get the weather for a city.", func(ctx context.Context, city string) (string, error) {
		return "72F and clear in " + city, nil
	})
	if err != nil {
		log.Fatal(err)
	}

	runner, err := goagent.New(
		goagent.WithInstructions("Give practical weather advice."),
		goagent.WithModel(goagent.ModelFromSimple(&weatherModel{})),
		goagent.WithTools(weather),
		goagent.WithEventSinks(goagent.EventSinkFunc(func(ctx context.Context, event goagent.Event) {
			if event.Kind == goagent.EventStop {
				log.Printf("run stopped: %s", event.StopReason)
			}
		})),
	)
	if err != nil {
		log.Fatal(err)
	}

	result, err := runner.Run(ctx, goagent.RunRequest{Input: "Should I bring a jacket to Austin tonight?"})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println(result.Text)
}

type weatherModel struct {
	calledTool bool
}

func (m *weatherModel) Turn(ctx context.Context, request goagent.TurnRequest) (goagent.TurnResult, error) {
	if !m.calledTool {
		m.calledTool = true
		return goagent.TurnResult{ToolCalls: []goagent.ToolCall{{
			ID:    "call-weather-1",
			Name:  "weather",
			Input: json.RawMessage(`{"city":"Austin"}`),
		}}}, nil
	}

	forecast := "unknown"
	for _, message := range request.Messages {
		if message.Role == goagent.RoleTool && message.ToolCallID == "call-weather-1" {
			forecast = message.Content
			break
		}
	}

	return goagent.TurnResult{
		Message: goagent.Message{
			Role:    goagent.RoleAssistant,
			Content: "Bring a light jacket. The forecast is " + forecast + ".",
		},
		StopReason: goagent.StopComplete,
	}, nil
}
