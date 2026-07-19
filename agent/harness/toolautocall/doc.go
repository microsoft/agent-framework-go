// Copyright (c) Microsoft. All rights reserved.

// Package toolautocall provides the middleware that automatically invokes the
// function tools a model calls and feeds their results back for the next turn.
// Providers add it by default unless agent.Config.DisableFuncAutoCall is set.
package toolautocall
