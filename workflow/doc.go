// Copyright (c) Microsoft. All rights reserved.

// Package workflow is a graph-execution engine for multi-agent orchestration.
//
// A Builder wires ExecutorBindings into a graph — sequential, concurrent,
// conditional, fan-out/fan-in, and subworkflows — and execution advances via a
// TurnToken. Per-executor private scopes and named shared scopes hold state and
// underpin checkpointing. This engine is distinct from single-agent runs in the
// agent package.
package workflow
