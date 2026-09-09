// Copyright (c) Microsoft. All rights reserved.

// Package toolautocall provides the middleware that automatically invokes the
// function tools a model calls and feeds their results back for the next turn.
// Supporting providers add it by default and expose [Config] for customization.
package toolautocall
