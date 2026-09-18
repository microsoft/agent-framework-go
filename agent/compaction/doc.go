// Copyright (c) Microsoft. All rights reserved.

// Package compaction reduces conversation history to fit a model's context
// window. It provides history and context providers plus composable strategies
// — sliding window, truncation, summarization, tool-result eviction, and
// context-window sizing — selected by triggers evaluated over a message index.
package compaction
