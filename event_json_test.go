package goagent_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	goagent "github.com/jgabor/go-agent"
)

func TestMarshalEventsUnmarshalEventsRoundTrip(t *testing.T) {
	events := []goagent.Event{
		{Sequence: 1, Kind: goagent.EventResponseStart, RunID: "run-a", ParentRunID: "p1", TaskID: "t1", Metadata: map[string]string{"k": "v"}},
		{Sequence: 2, Kind: goagent.EventTextDelta, RunID: "run-a", ParentRunID: "p1", TaskID: "t1", Metadata: map[string]string{"k": "v"}, Text: "hi"},
		{Sequence: 3, Kind: goagent.EventStop, RunID: "run-a", ParentRunID: "p1", TaskID: "t1", Metadata: map[string]string{"k": "v"}, StopReason: goagent.StopComplete},
	}
	data, err := goagent.MarshalEvents(events)
	if err != nil {
		t.Fatal(err)
	}
	var wrap struct {
		V int `json:"v"`
	}
	if err := json.Unmarshal(data, &wrap); err != nil || wrap.V != 1 {
		t.Fatalf("envelope v = %v err=%v", wrap.V, err)
	}
	got, err := goagent.UnmarshalEvents(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, events) {
		t.Fatalf("events mismatch\n got: %#v\nwant: %#v", got, events)
	}
}

func TestEventsJSONReplayAssemblesSameOutcome(t *testing.T) {
	// message_final with empty Message matches assembled state when no content blocks ran.
	events := []goagent.Event{
		{Sequence: 1, Kind: goagent.EventResponseStart, RunID: "r1"},
		{Sequence: 2, Kind: goagent.EventMessageFinal, RunID: "r1"},
		{Sequence: 3, Kind: goagent.EventStop, RunID: "r1", StopReason: goagent.StopComplete},
	}
	orig, err := goagent.AssembleEvents(events)
	if err != nil {
		t.Fatal(err)
	}
	data, err := goagent.MarshalEvents(events)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := goagent.UnmarshalEvents(data)
	if err != nil {
		t.Fatal(err)
	}
	again, err := goagent.AssembleEvents(replayed)
	if err != nil {
		t.Fatal(err)
	}
	if orig.Text != again.Text || orig.StopReason != again.StopReason || len(orig.Messages) != len(again.Messages) {
		t.Fatalf("assembled mismatch orig=%+v again=%+v", orig, again)
	}
}

func TestToJSONEventPreservesErrorMessage(t *testing.T) {
	e := goagent.Event{Sequence: 1, Kind: goagent.EventError, Err: errors.New("boom"), RunID: "r1"}
	j := goagent.ToJSONEvent(e)
	if j.ErrorMessage != "boom" {
		t.Fatalf("ErrorMessage = %q", j.ErrorMessage)
	}
	back := goagent.FromJSONEvent(j)
	if back.Err == nil || back.Err.Error() != "boom" {
		t.Fatalf("Err = %v", back.Err)
	}
}

func TestMarshalEventsSanitizesDecisionSessionValues(t *testing.T) {
	bad := make(chan int)
	events := []goagent.Event{
		{
			Sequence: 1,
			Kind:     goagent.EventPolicyPending,
			Decision: goagent.Decision{
				Kind: goagent.DecisionRunStart,
				Request: goagent.RunRequest{
					Session: goagent.Session{Values: map[string]any{"bad": bad, "requestOK": "yes"}},
				},
				Session: goagent.Session{Values: map[string]any{"bad": bad, "ok": "kept"}},
			},
		},
	}
	data, err := goagent.MarshalEvents(events)
	if err != nil {
		t.Fatal(err)
	}
	got, err := goagent.UnmarshalEvents(data)
	if err != nil {
		t.Fatal(err)
	}
	values := got[0].Decision.Session.Values
	if values["ok"] != "kept" {
		t.Fatalf("sanitized values = %#v", values)
	}
	if _, ok := values["bad"]; ok {
		t.Fatalf("non-JSON value survived sanitization: %#v", values)
	}
	requestValues := got[0].Decision.Request.Session.Values
	if requestValues["requestOK"] != "yes" {
		t.Fatalf("sanitized request values = %#v", requestValues)
	}
}

func TestUnmarshalEventsRejectsUnknownVersion(t *testing.T) {
	data := []byte(`{"v":99,"events":[]}`)
	_, err := goagent.UnmarshalEvents(data)
	if err == nil {
		t.Fatal("expected error")
	}
}
