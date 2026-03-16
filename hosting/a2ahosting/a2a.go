// Copyright (c) Microsoft. All rights reserved.

package a2ahosting

import (
	"log/slog"
	"net/http"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2asrv"
	"github.com/microsoft/agent-framework-go/agent"
)

type HandlerConfig struct {
	Agent *agent.Agent

	AgentCard *a2a.AgentCard
	Logger    *slog.Logger
	RunMode   AgentRunMode
	TaskStore a2asrv.TaskStore
}

func NewRequestHandler(cfg HandlerConfig) a2asrv.RequestHandler {
	if cfg.Agent == nil {
		panic("agent is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.RunMode.value == "" {
		cfg.RunMode = DisallowBackground()
	}

	options := []a2asrv.RequestHandlerOption{a2asrv.WithLogger(cfg.Logger)}
	if cfg.TaskStore != nil {
		options = append(options, a2asrv.WithTaskStore(cfg.TaskStore))
	}
	if cfg.AgentCard != nil {
		options = append(options, a2asrv.WithExtendedAgentCard(cfg.AgentCard))
	}

	return a2asrv.NewHandler(newExecutor(cfg.Agent, cfg.RunMode), options...)
}

func NewHTTPHandler(cfg HandlerConfig) http.Handler {
	return a2asrv.NewJSONRPCHandler(NewRequestHandler(cfg))
}
