// Copyright (c) Microsoft. All rights reserved.

// Package message defines the message and content primitives that are the
// universal currency of the framework.
//
// A [Message] carries a [Role] and an ordered list of typed [Content] parts —
// text, reasoning, data and URI references, function calls and results, usage,
// and annotations, among others. Agent input and output, tool results, and
// workflow payloads all flow as messages. Content parts marshal to and from a
// discriminated JSON representation so messages can be persisted and replayed.
package message
