// Copyright (c) Microsoft. All rights reserved.

package memory

import (
	"context"

	"github.com/microsoft/agent-framework-go/message"
)

// Thread contains the state of a specific conversation with an agent which may include:
//
//   - Conversation history or a reference to externally stored conversation history.
//   - Memories or a reference to externally stored memories.
//   - Any other state that the agent needs to persist across runs for a conversation.
//
// A Thread may also have behaviors attached to it that may include:
//
//   - Customized storage of state.
//   - Data extraction from and injection into a conversation.
//   - Chat history reduction, e.g. where messages needs to be summarized or truncated to reduce the size.
//
// A Thread is always constructed by an [agent.Agent] so that the [agent.Agent] can attach any necessary behaviors to the Thread.
// See the [agent.Agent.NewThread] and [agent.Agent.UnmarshalThread] methods for more information.
//
// Because of these behaviors, a Thread may not be reusable across different agents, since each agent may add different
// behaviors to the Thread it creates.
//
// To support conversations that may need to survive application restarts or separate service requests,
// a Thread can be serialized and deserialized, so that it can be saved in a persistent store.
type Thread interface {
	// MessagesReceived adds messages to the thread.
	MessagesReceived(ctx context.Context, messages ...*message.Message) error
}
