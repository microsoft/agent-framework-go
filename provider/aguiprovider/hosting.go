// Copyright (c) Microsoft. All rights reserved.

package aguiprovider

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"log/slog"
	"net/http"

	aguiEvents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	aguiTypes "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	aguiSSE "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/encoding/sse"
	"github.com/microsoft/agent-framework-go/agent"
)

// HandlerConfig contains configuration for [NewJSONHTTPHandler].
type HandlerConfig struct {
	// Logger receives handler diagnostics. When nil, logs are discarded.
	Logger *slog.Logger
}

// NewJSONHTTPHandler returns an [http.Handler] that hosts hostedAgent behind the
// AG-UI protocol: it accepts POSTed AG-UI run input and streams the agent's
// response back as AG-UI Server-Sent Events. It panics if hostedAgent is nil.
func NewJSONHTTPHandler(hostedAgent *agent.Agent, cfg HandlerConfig) http.Handler {
	if hostedAgent == nil {
		panic("agent is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	writer := aguiSSE.NewSSEWriter().WithLogger(cfg.Logger)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		input, err := decodeInput(r)
		if err != nil {
			http.Error(w, "invalid input", http.StatusBadRequest)
			return
		}

		threadID := input.ThreadID
		if threadID == "" {
			threadID = aguiEvents.GenerateThreadID()
		}
		runID := input.RunID
		if runID == "" {
			runID = aguiEvents.GenerateRunID()
		}

		messagesIn, err := toAgentMessages(input.Messages)
		if err != nil {
			http.Error(w, "invalid messages", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache,no-store")
		w.Header().Set("Pragma", "no-cache")

		var runOptions []agent.Option
		for _, t := range toDeclarationTools(input.Tools) {
			runOptions = append(runOptions, agent.WithTool(t))
		}
		updates := hostedAgent.Run(r.Context(), messagesIn, runOptions...)
		updatesSeq := iter.Seq2[*agent.ResponseUpdate, error](updates)
		events := updatesToAGUIEvents(r.Context(), updatesSeq, threadID, runID, toClientToolNames(input.Tools))

		if err := streamEvents(r.Context(), w, writer, events); err != nil {
			// Response has already started as SSE, so we cannot switch to http.Error here.
			cfg.Logger.ErrorContext(r.Context(), "agui stream failed", "error", err)
		}
	})
}

func decodeInput(r *http.Request) (*aguiTypes.RunAgentInput, error) {
	if r == nil || r.Body == nil {
		return nil, errors.New("request body is required")
	}
	defer func() { _ = r.Body.Close() }()

	var input aguiTypes.RunAgentInput
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&input); err != nil {
		return nil, err
	}
	return &input, nil
}

func streamEvents(
	ctx context.Context,
	w http.ResponseWriter,
	writer *aguiSSE.SSEWriter,
	events iter.Seq2[aguiEvents.Event, error],
) error {
	for evt, err := range events {
		if err != nil {
			_ = writer.WriteErrorEvent(ctx, w, err, "")
			return err
		}
		if evt == nil {
			continue
		}
		if writeErr := writer.WriteEvent(ctx, w, evt); writeErr != nil {
			return writeErr
		}
	}
	return nil
}
