// Copyright (c) Microsoft. All rights reserved.

// Package skills provides domain knowledge bases ("skills") an agent can load
// on demand. A skill bundles instructional content with named resources and
// runnable scripts, surfaced to the agent as tools through a context provider.
//
// Source discovery is cached by default, while filters run for each invocation.
// Sources and filters receive agent and session identity in [SourceContext].
// Scripts accept JSON values through [Script.Run]. Providers support opt-in
// source ownership and explicit cleanup; see [ContextProvider.Close].
//
// [AggregatingSource], [CachingSource], [FilteringSource], and
// [DeduplicatingSource] can also be composed independently of a provider.
package skills
