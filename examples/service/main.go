package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	goagent "github.com/jgabor/go-agent"
)

func main() {
	server, err := newServer()
	if err != nil {
		log.Fatal(err)
	}

	log.Println("listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", server.routes()))
}

type server struct {
	runner goagent.Runner
}

func newServer() (*server, error) {
	weather, err := goagent.NewTool("weather", "Get the weather for a city.", func(ctx context.Context, input weatherInput) (string, error) {
		if strings.EqualFold(input.City, "forbidden") {
			return "", errors.New("city is not available")
		}
		unit := input.Unit
		if unit == "" {
			unit = "F"
		}
		return "72" + unit + " and clear in " + input.City, nil
	})
	if err != nil {
		return nil, err
	}

	runner, err := goagent.New(
		goagent.WithInstructions("Answer as a concise weather assistant."),
		goagent.WithModel(&weatherModel{}),
		goagent.WithTools(weather),
		goagent.WithSessionStore(goagent.NewMemorySessionStore()),
		goagent.WithPolicy(servicePolicy),
		goagent.WithEventSinks(goagent.EventSinkFunc(logStopEvents)),
	)
	if err != nil {
		return nil, err
	}

	return &server{runner: runner}, nil
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ask", s.ask)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	return mux
}

func (s *server) ask(w http.ResponseWriter, r *http.Request) {
	var request askRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if request.Input == "" {
		http.Error(w, "input is required", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	result, err := s.runner.Run(ctx, goagent.RunRequest{
		Input:     request.Input,
		SessionID: request.SessionID,
		MaxSteps:  4,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if result.StopReason != goagent.StopComplete {
		http.Error(w, "run stopped: "+string(result.StopReason), http.StatusBadGateway)
		return
	}

	writeJSON(w, askResponse{Text: result.Text, SessionID: result.Session.ID})
}

type askRequest struct {
	Input     string `json:"input"`
	SessionID string `json:"session_id"`
}

type askResponse struct {
	Text      string `json:"text"`
	SessionID string `json:"session_id,omitempty"`
}

type weatherInput struct {
	City string `json:"city" description:"City name."`
	Unit string `json:"unit,omitempty" description:"Temperature unit."`
}

type weatherModel struct{}

func (m *weatherModel) Turn(ctx context.Context, request goagent.TurnRequest) (goagent.TurnResult, error) {
	latestUser := latestUserIndex(request.Messages)
	for _, message := range request.Messages[latestUser+1:] {
		if message.Role == goagent.RoleTool && message.ToolCallID == "call-weather-1" {
			return goagent.TurnResult{
				Message:    goagent.Message{Role: goagent.RoleAssistant, Content: "Forecast: " + message.Content + "."},
				StopReason: goagent.StopComplete,
			}, nil
		}
	}

	city := cityFromInput(request.Messages)
	return goagent.TurnResult{ToolCalls: []goagent.ToolCall{{
		ID:    "call-weather-1",
		Name:  "weather",
		Input: json.RawMessage(`{"city":"` + city + `","unit":"F"}`),
	}}}, nil
}

func cityFromInput(messages []goagent.Message) string {
	if len(messages) == 0 {
		return "Austin"
	}
	last := messages[latestUserIndex(messages)].Content
	for _, city := range []string{"Austin", "Berlin", "Tokyo"} {
		if strings.Contains(strings.ToLower(last), strings.ToLower(city)) {
			return city
		}
	}
	return "Austin"
}

func latestUserIndex(messages []goagent.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == goagent.RoleUser {
			return i
		}
	}
	return len(messages) - 1
}

var servicePolicy = goagent.PolicyFunc(func(ctx context.Context, decision goagent.Decision) (goagent.PolicyDecision, error) {
	if decision.Kind == goagent.DecisionToolCall && decision.ToolCall.Name != "weather" {
		return goagent.PolicyDecision{Allowed: false, Reason: "unknown tool"}, nil
	}
	return goagent.PolicyDecision{Allowed: true}, nil
})

func logStopEvents(ctx context.Context, event goagent.Event) {
	if event.Kind == goagent.EventStop {
		log.Printf("run_id=%s stop=%s", event.RunID, event.StopReason)
	}
}

func writeJSON(w http.ResponseWriter, response any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Printf("encode response: %v", err)
	}
}
