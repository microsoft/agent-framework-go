// Copyright (c) Microsoft. All rights reserved.

// Package openaiprovider provides OpenAI-backed agents. [NewAgent] uses the
// OpenAI API that currently fits the framework best (the Responses API, via
// [NewResponsesAgent]); [NewChatCompletionsAgent] targets the Chat Completions
// API instead. Both take an OpenAI client and an [AgentConfig].
package openaiprovider
