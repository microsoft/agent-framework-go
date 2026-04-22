// Copyright (c) Microsoft. All rights reserved.

package a2ahosting

import (
	"net/http"

	"github.com/a2aproject/a2a-go/v2/a2asrv"
)

func NewRequestHandler(cfg ExecutorConfig, options ...a2asrv.RequestHandlerOption) a2asrv.RequestHandler {
	if cfg.Agent == nil {
		panic("agent is required")
	}
	return a2asrv.NewHandler(NewExecutor(cfg), options...)
}

func NewHTTPHandler(cfg ExecutorConfig, options ...a2asrv.RequestHandlerOption) http.Handler {
	return a2asrv.NewJSONRPCHandler(NewRequestHandler(cfg, options...))
}
