// Copyright (c) Microsoft. All rights reserved.

// Package agentopts shares internal run options between agents and middleware.
package agentopts

// SessionlessHistory lets middleware retain history across its own iterations
// when the caller omitted a session and the agent uses default history.
type SessionlessHistory struct{}

// MAFValue implements agent.Option without depending on the agent package.
func (o SessionlessHistory) MAFValue() any { return o }
