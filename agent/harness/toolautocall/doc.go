// Copyright (c) Microsoft. All rights reserved.

// Package toolautocall provides the middleware that automatically invokes the
// function tools a model calls and feeds their results back for the next turn.
// Supporting providers add it by default and expose [Config] for customization.
//
// Tool tracing is backend-neutral. Set [Config.Tracer] to supply a [Tracer], or
// use [WithTracer] to supply one through the run context, including from agent
// middleware. An explicit [Config.Tracer] takes precedence over the context.
package toolautocall
