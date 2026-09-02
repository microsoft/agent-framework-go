// Copyright (c) Microsoft. All rights reserved.

// Package agent provides the provider-agnostic agent abstraction at the core of
// the framework.
//
// An [Agent] is built from a [ProviderConfig] whose [ProviderConfig.Run] is a
// streaming provider function; around it the agent layers conversation history,
// context providers, and middleware. Agents exchange
// [github.com/microsoft/agent-framework-go/message.Message] values and are run
// with [Agent.Run], [Agent.RunText], or [Agent.RunMessage], each returning a
// streaming [ResponseStream]. Concrete agents are normally created through a
// provider constructor (see the provider packages) rather than directly.
package agent
