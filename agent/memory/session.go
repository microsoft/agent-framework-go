// Copyright (c) Microsoft. All rights reserved.

package memory

// Session contains the state of a specific conversation with an agent which may include:
//
//   - Conversation history or a reference to externally stored conversation history.
//   - Memories or a reference to externally stored memories.
//   - Any other state that the agent needs to persist across runs for a conversation.
//
// A Session may also have behaviors attached to it that may include:
//
//   - Customized storage of state.
//   - Data extraction from and injection into a conversation.
//   - Chat history reduction, e.g. where messages needs to be summarized or truncated to reduce the size.
//
// A Session is always constructed by an [agent.Agent] so that the [agent.Agent] can attach any necessary behaviors to the Session.
// See the [agent.Agent.CreateSession], [agent.Agent.MarshalSession], and [agent.Agent.UnmarshalSession] methods for more information.
//
// Because of these behaviors, a Session may not be reusable across different agents, since each agent may add different
// behaviors to the Session it creates.
//
// To support conversations that may need to survive application restarts or separate service requests,
// a Session can be serialized and deserialized, so that it can be saved in a persistent store.
type Session interface {
	// GetStateBag returns the session's [StateBag] for storing session-scoped provider state.
	GetStateBag() *StateBag
}
