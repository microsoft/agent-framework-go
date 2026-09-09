// Copyright (c) Microsoft. All rights reserved.

package foundryprovider_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

func TestHostedAgentSessionIDRoundTripsThroughSessionJSON(t *testing.T) {
	session := &agent.Session{}
	foundryprovider.SetHostedAgentSessionID(session, " hosted-session-123 ")

	data, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}

	var restored agent.Session
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}

	if got := foundryprovider.HostedAgentSessionID(&restored); got != "hosted-session-123" {
		t.Fatalf("HostedAgentSessionID = %q, want hosted-session-123", got)
	}
}

func TestWithHostedAgentSessionIDStampsRequestAndPersistsOnSession(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := jsonMap(t, mustReadBody(t, r))
		if got := body["agent_session_id"]; got != "hosted-session-123" {
			t.Fatalf("agent_session_id = %#v", got)
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{
		Config: agent.Config{},
	})
	session, err := foundryAgent.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := foundryAgent.RunText(t.Context(), "hello", agent.WithSession(session), foundryprovider.WithHostedAgentSessionID(" hosted-session-123 ")).Collect(); err != nil {
		t.Fatalf("RunText error = %v", err)
	}
	if got := foundryprovider.HostedAgentSessionID(session); got != "hosted-session-123" {
		t.Fatalf("HostedAgentSessionID = %q, want hosted-session-123", got)
	}
}

func TestHostedAgentSessionIDCapturedFromResponseAndReused(t *testing.T) {
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		body := jsonMap(t, mustReadBody(t, r))
		switch requests {
		case 1:
			if _, ok := body["agent_session_id"]; ok {
				t.Fatalf("first request unexpectedly included agent_session_id: %#v", body["agent_session_id"])
			}
			w.Header().Set("x-agent-session-id", "hosted-session-456")
		case 2:
			if got := body["agent_session_id"]; got != "hosted-session-456" {
				t.Fatalf("agent_session_id = %#v", got)
			}
		default:
			t.Fatalf("unexpected request %d", requests)
		}
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{
		Config: agent.Config{},
	})
	session, err := foundryAgent.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if _, err := foundryAgent.RunText(t.Context(), "hello", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("first RunText error = %v", err)
	}
	if got := foundryprovider.HostedAgentSessionID(session); got != "hosted-session-456" {
		t.Fatalf("HostedAgentSessionID after first run = %q, want hosted-session-456", got)
	}

	if _, err := foundryAgent.RunText(t.Context(), "hello again", agent.WithSession(session)).Collect(); err != nil {
		t.Fatalf("second RunText error = %v", err)
	}
}

func TestWithHostedAgentSessionIDRejectsSessionConflict(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("request should not be sent when hosted session IDs conflict")
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{
		Config: agent.Config{},
	})
	session, err := foundryAgent.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	foundryprovider.SetHostedAgentSessionID(session, "hosted-session-a")

	_, err = foundryAgent.RunText(t.Context(), "hello", agent.WithSession(session), foundryprovider.WithHostedAgentSessionID("hosted-session-b")).Collect()
	if err == nil {
		t.Fatal("RunText error = nil, want conflict error")
	}
	if !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("RunText error = %v, want conflict message", err)
	}
}

func TestHostedAgentSessionIDRejectsUnexpectedHeaderSwitch(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := jsonMap(t, mustReadBody(t, r))
		if got := body["agent_session_id"]; got != "hosted-session-a" {
			t.Fatalf("agent_session_id = %#v", got)
		}
		w.Header().Set("x-agent-session-id", "hosted-session-b")
		writeResponsesOK(w)
	}))
	defer server.Close()

	foundryAgent := newFoundryAgent(t, server, foundryprovider.ModelDeployment("gpt-4o-mini"), foundryprovider.AgentConfig{
		Config: agent.Config{},
	})
	session, err := foundryAgent.CreateSession(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	foundryprovider.SetHostedAgentSessionID(session, "hosted-session-a")

	_, err = foundryAgent.RunText(t.Context(), "hello", agent.WithSession(session)).Collect()
	if err == nil {
		t.Fatal("RunText error = nil, want hosted session switch error")
	}
	if !strings.Contains(err.Error(), "unexpected hosted-agent session switch") {
		t.Fatalf("RunText error = %v, want switch error", err)
	}
	if got := foundryprovider.HostedAgentSessionID(session); got != "hosted-session-a" {
		t.Fatalf("HostedAgentSessionID after failed run = %q, want hosted-session-a", got)
	}
}

func TestHostedAgentSessionIDHelpersRejectInvalidArguments(t *testing.T) {
	assertPanics(t, func() { foundryprovider.SetHostedAgentSessionID(&agent.Session{}, " ") })
}
