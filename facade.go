package goagent

import (
	"fmt"
	"time"
)

// Option configures New with already-resolved runtime dependencies.
type Option func(*Agent) error

// New builds a Runner from already-resolved runtime dependencies.
//
// New is the low-ceremony facade over Agent and NewRunner. It does not resolve
// provider credentials, load settings, discover prompts or resources, or assemble
// product lifecycle concerns for the host application.
func New(options ...Option) (Runner, error) {
	agent := Agent{}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("goagent: option cannot be nil")
		}
		if err := option(&agent); err != nil {
			return nil, err
		}
	}
	return NewRunner(agent)
}

// WithModel sets the required model dependency for New.
func WithModel(model Model) Option {
	return func(agent *Agent) error {
		if model == nil {
			return fmt.Errorf("goagent: model cannot be nil")
		}
		agent.Model = model
		return nil
	}
}

// WithInstructions sets system instructions for New.
func WithInstructions(instructions string) Option {
	return func(agent *Agent) error {
		agent.Instructions = instructions
		return nil
	}
}

// WithTools sets the resolved tool dependencies for New.
func WithTools(tools ...Tool) Option {
	return func(agent *Agent) error {
		agent.Tools = append([]Tool(nil), tools...)
		return nil
	}
}

// WithPolicy sets the host-owned policy dependency for New.
func WithPolicy(policy Policy) Option {
	return func(agent *Agent) error {
		if policy == nil {
			return fmt.Errorf("goagent: policy cannot be nil")
		}
		agent.Policy = policy
		return nil
	}
}

// WithSessionStore sets the host-owned session store dependency for New.
func WithSessionStore(store SessionStore) Option {
	return func(agent *Agent) error {
		if store == nil {
			return fmt.Errorf("goagent: session store cannot be nil")
		}
		agent.SessionStore = store
		return nil
	}
}

// WithEventSinks sets event sinks that observe runtime events from New.
func WithEventSinks(sinks ...EventSink) Option {
	return func(agent *Agent) error {
		agent.EventSinks = append([]EventSink(nil), sinks...)
		return nil
	}
}

// WithRetry enables bounded retry for model and runtime-owned failures.
func WithRetry(retry RetryPolicy) Option {
	return func(agent *Agent) error {
		if retry.MaxAttempts < 0 {
			return fmt.Errorf("goagent: retry max attempts cannot be negative")
		}
		if retry.Delay < 0*time.Second {
			return fmt.Errorf("goagent: retry delay cannot be negative")
		}
		agent.Retry = retry
		return nil
	}
}
