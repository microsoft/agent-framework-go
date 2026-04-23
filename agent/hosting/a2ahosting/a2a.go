// Copyright (c) Microsoft. All rights reserved.

package a2ahosting

import (
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

// NewRequestHandler creates a new request handler.
func NewRequestHandler(cfg ExecutorConfig, options ...a2asrv.RequestHandlerOption) a2asrv.RequestHandler {
	if cfg.Agent == nil {
		panic("agent is required")
	}
	return a2asrv.NewHandler(NewExecutor(cfg), options...)
}

// NewJSONRPCHandler creates an [http.Handler] which implements JSONRPC A2A protocol binding.
func NewJSONRPCHandler(cfg ExecutorConfig, options ...a2asrv.RequestHandlerOption) http.Handler {
	return a2asrv.NewJSONRPCHandler(NewRequestHandler(cfg, options...))
}

// NewRESTHandler creates an [http.Handler] which implements the HTTP+JSON A2A protocol binding.
func NewJSONHTTPHandler(cfg ExecutorConfig, options ...a2asrv.RequestHandlerOption) http.Handler {
	return a2asrv.NewRESTHandler(NewRequestHandler(cfg, options...))
}
