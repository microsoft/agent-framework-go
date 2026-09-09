// Copyright (c) Microsoft. All rights reserved.

package foundryprovider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"net/http"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/openai/openai-go/v3/option"
)

const (
	hostedAgentSessionIDStateKey = "foundryprovider.hostedAgentSessionID"
	hostedAgentSessionIDHeader   = "x-agent-session-id"
)

type hostedAgentSessionIDContextKey struct{}

type hostedAgentSessionIDBox struct {
	value string
}

type hostedAgentSessionIDOpt string

func (o hostedAgentSessionIDOpt) MAFValue() any { return string(o) }

// WithHostedAgentSessionID pins a Foundry hosted-agent session ID for a single run.
//
// When the supplied [agent.Session] already stores a different hosted-agent session
// ID, the run fails rather than silently switching sandboxes.
func WithHostedAgentSessionID(hostedSessionID string) agent.Option {
	return hostedAgentSessionIDOpt(strings.TrimSpace(hostedSessionID))
}

// HostedAgentSessionID returns the sticky Foundry hosted-agent session ID stored in session.
func HostedAgentSessionID(session *agent.Session) string {
	if session == nil {
		return ""
	}
	var hostedSessionID string
	if ok, err := session.Get(hostedAgentSessionIDStateKey, &hostedSessionID); err != nil || !ok {
		return ""
	}
	return strings.TrimSpace(hostedSessionID)
}

// SetHostedAgentSessionID stores a sticky Foundry hosted-agent session ID in session.
//
// The stored value is serialized with the session and is sent automatically on later
// Foundry runs that reuse the same session.
func SetHostedAgentSessionID(session *agent.Session, hostedSessionID string) {
	if session == nil {
		return
	}
	session.Set(hostedAgentSessionIDStateKey, validateHostedAgentSessionID(hostedSessionID))
}

type hostedAgentSessionMiddleware struct{}

func (hostedAgentSessionMiddleware) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, options ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	session, _ := agent.GetOption(options, agent.WithSession)
	hostedSessionID, err := resolveHostedAgentSessionID(session, options)
	if err != nil {
		return func(yield func(*agent.ResponseUpdate, error) bool) {
			yield(nil, err)
		}
	}
	box := &hostedAgentSessionIDBox{value: hostedSessionID}
	ctx = context.WithValue(ctx, hostedAgentSessionIDContextKey{}, box)
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		defer applyHostedAgentSessionID(session, box)
		for update, err := range next(ctx, messages, options...) {
			if !yield(update, err) {
				return
			}
		}
	}
}

func hostedAgentSessionRequestOption() option.RequestOption {
	return option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		if box, ok := req.Context().Value(hostedAgentSessionIDContextKey{}).(*hostedAgentSessionIDBox); ok && box.value != "" {
			if err := setHostedAgentSessionIDRequestBody(req, box.value); err != nil {
				return nil, err
			}
		}

		resp, err := next(req)
		if err != nil || resp == nil {
			return resp, err
		}
		if box, ok := req.Context().Value(hostedAgentSessionIDContextKey{}).(*hostedAgentSessionIDBox); ok {
			if err := captureHostedAgentSessionID(resp, box); err != nil {
				_ = resp.Body.Close()
				return nil, err
			}
		}
		return resp, nil
	})
}

func resolveHostedAgentSessionID(session *agent.Session, options []agent.Option) (string, error) {
	sessionHostedSessionID := HostedAgentSessionID(session)
	runHostedSessionID := runHostedAgentSessionID(options)
	if runHostedSessionID == "" {
		return sessionHostedSessionID, nil
	}
	if sessionHostedSessionID == "" || sessionHostedSessionID == runHostedSessionID {
		return runHostedSessionID, nil
	}
	return "", fmt.Errorf("foundryprovider: run hosted-agent session ID %q conflicts with session hosted-agent session ID %q", runHostedSessionID, sessionHostedSessionID)
}

func runHostedAgentSessionID(options []agent.Option) string {
	hostedSessionID, _ := agent.GetOption(options, WithHostedAgentSessionID)
	return hostedSessionID
}

func applyHostedAgentSessionID(session *agent.Session, box *hostedAgentSessionIDBox) {
	if session == nil || box == nil || box.value == "" {
		return
	}
	SetHostedAgentSessionID(session, box.value)
}

func setHostedAgentSessionIDRequestBody(req *http.Request, hostedSessionID string) error {
	if req.Body == nil {
		return nil
	}
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return fmt.Errorf("foundryprovider: read request body: %w", err)
	}
	_ = req.Body.Close()
	if len(body) == 0 {
		body = []byte("{}")
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("foundryprovider: parse request body: %w", err)
	}
	if payload == nil {
		payload = map[string]any{}
	}
	payload["agent_session_id"] = hostedSessionID
	body, err = json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("foundryprovider: set agent_session_id request field: %w", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(body))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(body)), nil
	}
	req.ContentLength = int64(len(body))
	return nil
}

func captureHostedAgentSessionID(resp *http.Response, box *hostedAgentSessionIDBox) error {
	hostedSessionID := strings.TrimSpace(resp.Header.Get(hostedAgentSessionIDHeader))
	if hostedSessionID == "" {
		return nil
	}
	if box.value != "" && box.value != hostedSessionID {
		return fmt.Errorf("foundryprovider: unexpected hosted-agent session switch from %q to %q", box.value, hostedSessionID)
	}
	box.value = hostedSessionID
	return nil
}

func validateHostedAgentSessionID(hostedSessionID string) string {
	hostedSessionID = strings.TrimSpace(hostedSessionID)
	if hostedSessionID == "" {
		panic("hosted agent session ID is required")
	}
	return hostedSessionID
}
