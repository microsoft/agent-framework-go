// Copyright (c) Microsoft. All rights reserved.

// Package otelprovider instruments agents and embedding generators with
// OpenTelemetry tracing and metrics following the GenAI semantic conventions.
// Use [NewMiddleware] for agent runs and [WrapEmbeddingGenerator] for embedding
// generation. Both use the global OpenTelemetry providers.
package otelprovider
