package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServiceAskEndpoint(t *testing.T) {
	server, err := newServer()
	if err != nil {
		t.Fatal(err)
	}

	body := bytes.NewBufferString(`{"input":"Should I bring a jacket in Berlin?","session_id":"session-1"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/ask", body)
	server.routes().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var response askResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.SessionID != "session-1" {
		t.Fatalf("SessionID = %q", response.SessionID)
	}
	if !strings.Contains(response.Text, "Berlin") {
		t.Fatalf("Text = %q, want Berlin forecast", response.Text)
	}
}

func TestServiceAskEndpointResumesLatestSessionTurn(t *testing.T) {
	server, err := newServer()
	if err != nil {
		t.Fatal(err)
	}

	for _, input := range []string{
		"Should I bring a jacket in Berlin?",
		"What about Tokyo?",
	} {
		body := bytes.NewBufferString(`{"input":"` + input + `","session_id":"session-1"}`)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/ask", body)
		server.routes().ServeHTTP(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
		}

		if strings.Contains(input, "Tokyo") && !strings.Contains(recorder.Body.String(), "Tokyo") {
			t.Fatalf("second response did not use latest session turn: %s", recorder.Body.String())
		}
	}
}

func TestServiceAskEndpointRejectsInvalidRequests(t *testing.T) {
	server, err := newServer()
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/ask", bytes.NewBufferString(`{"input":""}`))
	recorder := httptest.NewRecorder()
	server.routes().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
}

func TestServiceHealthEndpoint(t *testing.T) {
	server, err := newServer()
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	recorder := httptest.NewRecorder()
	server.routes().ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}
