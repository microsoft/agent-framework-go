// Copyright (c) Microsoft. All rights reserved.

// Package otelprovider instruments agents and embedding generators with
// OpenTelemetry tracing and metrics following the GenAI semantic conventions.
// Use [NewMiddleware] for agent, function-tool, and supported model-adapter calls,
// and [WrapEmbeddingGenerator] for embedding generation. Model calls are
// instrumented by default; [MiddlewareConfig.DisableModelTracing] omits their
// spans and metrics. Both use the global OpenTelemetry providers and leave their
// lifetime to the caller.
//
// Model calls produce client spans with operation "chat", model identifiers,
// token usage, and errors. They are children of the agent span; tool execution
// remains outside those model spans. SDK retries and any remote server-side
// orchestration stay within the corresponding adapter-call span. Prompt content,
// tool arguments, and tool results are not recorded by model tracing.
// Adapters supply neutral model metadata; instrumentation stays in this package.
// Nested agents use their own middleware settings rather than inheriting model
// tracing from a parent. Native SDK instrumentation may produce additional spans
// under its own scope and is not disabled by this middleware.
package otelprovider
