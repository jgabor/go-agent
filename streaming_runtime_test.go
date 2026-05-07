package goagent_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	goagent "github.com/jgabor/go-agent"
)

func TestRuntimeConsumesCanonicalStreamForRunStreamAndSinks(t *testing.T) {
	model := streamingTextModel{parts: []string{"Bring ", "a jacket."}}
	var sinkEvents []goagent.Event
	runner, err := goagent.NewRunner(goagent.Agent{
		Model: model,
		EventSinks: []goagent.EventSink{goagent.EventSinkFunc(func(_ context.Context, event goagent.Event) {
			sinkEvents = append(sinkEvents, event)
		})},
	})
	if err != nil {
		t.Fatal(err)
	}

	runResult, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Weather?"})
	if err != nil {
		t.Fatal(err)
	}
	runAssembled, err := goagent.AssembleStream(runResult.Events, nil)
	if err != nil {
		t.Fatal(err)
	}
	if runAssembled.Text != runResult.Text || runAssembled.StopReason != runResult.StopReason {
		t.Fatalf("assembled run = %+v, result = %+v", runAssembled, runResult)
	}
	if !reflect.DeepEqual(eventKinds(sinkEvents), eventKinds(runResult.Events)) {
		t.Fatalf("sink kinds = %v, run kinds = %v", eventKinds(sinkEvents), eventKinds(runResult.Events))
	}

	streamRunner, err := goagent.NewRunner(goagent.Agent{Model: model})
	if err != nil {
		t.Fatal(err)
	}
	stream, err := streamRunner.Stream(context.Background(), goagent.RunRequest{Input: "Weather?"})
	if err != nil {
		t.Fatal(err)
	}
	var streamed []goagent.Event
	for event := range stream {
		streamed = append(streamed, event)
	}
	streamAssembled, err := goagent.AssembleStream(streamed, nil)
	if err != nil {
		t.Fatal(err)
	}
	if streamAssembled.Text != runAssembled.Text || streamAssembled.StopReason != runAssembled.StopReason || !reflect.DeepEqual(eventKinds(streamed), eventKinds(runResult.Events)) {
		t.Fatalf("stream assembled = %+v events=%v, run assembled = %+v events=%v", streamAssembled, eventKinds(streamed), runAssembled, eventKinds(runResult.Events))
	}
}

func TestSimpleModelAdapterStreamsFinalResponse(t *testing.T) {
	model := goagent.ModelFromSimple(goagent.SimpleModelFunc(func(context.Context, goagent.TurnRequest) (goagent.TurnResult, error) {
		return goagent.TurnResult{Message: goagent.Message{Content: "Done."}, Usage: goagent.Usage{InputTokens: 1, OutputTokens: 1}}, nil
	}))
	var events []goagent.Event
	if err := model.Stream(context.Background(), goagent.TurnRequest{}, func(event goagent.Event) { events = append(events, event) }); err != nil {
		t.Fatal(err)
	}
	assembled, err := goagent.AssembleStream(append(events, goagent.Event{Kind: goagent.EventStop, StopReason: goagent.StopComplete}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if assembled.Text != "Done." || assembled.Usage.TotalTokens != 0 || assembled.Usage.InputTokens != 1 {
		t.Fatalf("assembled adapter stream = %+v", assembled)
	}
}

func TestModelSetupFailureReturnsErrorWithoutAcceptedTranscript(t *testing.T) {
	setupErr := errors.New("setup failed")
	runner, err := goagent.NewRunner(goagent.Agent{Model: setupFailModel{err: setupErr}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Go"})
	if !errors.Is(err, setupErr) {
		t.Fatalf("error = %v, want %v", err, setupErr)
	}
	for _, event := range result.Events {
		if event.Kind == goagent.EventResponseStart || event.Kind == goagent.EventMessageFinal {
			t.Fatalf("setup failure emitted accepted transcript event: %+v", result.Events)
		}
	}
}

func TestAcceptedStreamFailureReturnsErrorAndTerminalEvents(t *testing.T) {
	streamErr := errors.New("provider stream failed")
	runner, err := goagent.NewRunner(goagent.Agent{Model: acceptedFailModel{err: streamErr}})
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.Run(context.Background(), goagent.RunRequest{Input: "Go"})
	if !errors.Is(err, streamErr) {
		t.Fatalf("error = %v, want %v", err, streamErr)
	}
	assembled, assembleErr := goagent.AssembleStream(result.Events, streamErr)
	if assembleErr != nil && !errors.Is(assembleErr, streamErr) {
		t.Fatalf("assembled error = %v", assembleErr)
	}
	if !errors.Is(assembled.Err, streamErr) || assembled.StopReason != goagent.StopModelError {
		t.Fatalf("assembled terminal result = %+v", assembled)
	}
	assertEventKinds(t, result.Events, []goagent.EventKind{goagent.EventResponseStart, goagent.EventError, goagent.EventStop})
}

type streamingTextModel struct {
	parts []string
}

func (m streamingTextModel) Stream(_ context.Context, _ goagent.TurnRequest, emit func(goagent.Event)) error {
	emit(goagent.Event{Kind: goagent.EventResponseStart, MessageID: "message-1"})
	emit(goagent.Event{Kind: goagent.EventContentBlockStart, MessageID: "message-1", BlockID: "block-1", BlockKind: goagent.BlockText})
	text := ""
	for _, part := range m.parts {
		text += part
		emit(goagent.Event{Kind: goagent.EventTextDelta, BlockID: "block-1", Text: part})
	}
	emit(goagent.Event{Kind: goagent.EventContentBlockEnd, BlockID: "block-1", BlockKind: goagent.BlockText, Text: text})
	emit(goagent.Event{Kind: goagent.EventMessageFinal, MessageID: "message-1", Message: goagent.Message{Role: goagent.RoleAssistant, Content: text, Blocks: []goagent.Block{{ID: "block-1", Kind: goagent.BlockText, Text: text}}}})
	return nil
}

type setupFailModel struct {
	err error
}

func (m setupFailModel) Stream(context.Context, goagent.TurnRequest, func(goagent.Event)) error {
	return m.err
}

type acceptedFailModel struct {
	err error
}

func (m acceptedFailModel) Stream(_ context.Context, _ goagent.TurnRequest, emit func(goagent.Event)) error {
	emit(goagent.Event{Kind: goagent.EventResponseStart})
	emit(goagent.Event{Kind: goagent.EventError, Err: m.err})
	emit(goagent.Event{Kind: goagent.EventStop, StopReason: goagent.StopModelError})
	return m.err
}
