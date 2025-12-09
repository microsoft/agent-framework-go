package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/agent/chatagent"
	"github.com/microsoft/agent-framework/go/memory"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/openai"
)

func main() {
	var a *chatagent.Agent
	a = openai.NewChatAgent(openai.ClientConfig{
		Model: "gpt-4o-mini",
	}, &chatagent.Options{
		Instructions:       "You are a friendly assistant. Always address the user by their name.",
		NewContextProvider: func() memory.ContextProvider { return &UserInfoMemory{Agent: a} },
	})

	fmt.Println(">> Use thread with blank memory")

	thread := a.NewThread()

	fmt.Println(must(agent.RunText(a, "Hello, what is the square root of 9?", agent.WithThread(thread))))

	fmt.Println(must(agent.RunText(a, "My name is Ruaidhrí", agent.WithThread(thread))))

	fmt.Println(must(agent.RunText(a, "I am 20 years old", agent.WithThread(thread))))

	// We can serialize the thread. The serialized state will include the state of the memory component.
	serializedThread := must(json.Marshal(thread))

	fmt.Println(">> Use new thread with previously created memories")

	deserializedThread := must(a.UnmarshalThread(serializedThread))

	fmt.Println(must(agent.RunText(a, "What is my name and age?", agent.WithThread(deserializedThread))))
}

type UserInfo struct {
	Name string
	Age  int
}

var _ memory.ContextProvider = (*UserInfoMemory)(nil)

type UserInfoMemory struct {
	UserInfo UserInfo
	Agent    *chatagent.Agent `json:"-"`
}

type reentrantCtxKey struct{}

func isReentrant(ctx context.Context) bool {
	return ctx.Value(reentrantCtxKey{}) != nil
}

func (u *UserInfoMemory) Invoked(ctx *memory.InvokedContext) error {
	if isReentrant(ctx.Context) {
		// Don't try to extract user info when re-entrant.
		return nil
	}
	if ctx.Error != nil {
		// Nothing to do if there was an error.
		return nil
	}
	if u.UserInfo.Age != 0 && u.UserInfo.Name != "" {
		// We already have the user info.
		return nil
	}
	if !slices.ContainsFunc(ctx.RequestMessages, func(msg *message.Message) bool { return msg.Role == message.RoleUser }) {
		// No user messages to extract info from.
		return nil
	}
	// To avoid infinite loops, we mark re-entrancy in the context.
	opts := []agent.Option{agent.WithContext(context.WithValue(ctx.Context, reentrantCtxKey{}, struct{}{}))}
	for _, msg := range ctx.RequestMessages {
		opts = append(opts, agent.WithMessage(msg))
	}
	opts = append(opts, agent.WithMessage(message.NewText("Extract the user's name and age from the message if present. If not present return empty values.")))
	// We ask the agent to extract the user info from the conversation so far.
	out, _, err := agent.RunFor[UserInfo](u.Agent, opts...)
	if err != nil {
		return err
	}
	u.UserInfo.Name = cmp.Or(u.UserInfo.Name, out.Name)
	u.UserInfo.Age = cmp.Or(u.UserInfo.Age, out.Age)
	return nil
}

func (u *UserInfoMemory) Invoking(ctx *memory.InvokingContext) (*memory.Context, error) {
	if isReentrant(ctx.Context) {
		// Don't provide context when re-entrant.
		return nil, nil
	}
	// If we don't already know the user's name and age, add instructions to ask for them, otherwise just provide what we have to the context.
	var instructions string
	if u.UserInfo.Name == "" {
		instructions = "Ask the user for their name and politely decline to answer any questions until they provide it."
	} else {
		instructions = fmt.Sprintf("The user's name is %s.", u.UserInfo.Name)
	}
	instructions += "\n"
	if u.UserInfo.Age == 0 {
		instructions += "Ask the user for their age and politely decline to answer any questions until they provide it."
	} else {
		instructions += fmt.Sprintf("The user's age is %d.", u.UserInfo.Age)
	}
	return &memory.Context{
		Instructions: instructions,
	}, nil
}

// must is a helper to panic on error for samples.
// In production code, handle errors appropriately.
func must[T any](resp T, err error) T {
	if err != nil {
		panic(err)
	}
	return resp
}
