package main

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/memory"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/openai"
)

func main() {
	var ag *agent.Agent
	ag = openai.NewChatAgent(openai.AgentConfig{
		Model:              "gpt-4o-mini",
		SystemInstructions: "You are a friendly assistant. Always address the user by their name.",
		NewContextProvider: func() memory.ContextProvider { return &UserInfoMemory{Agent: ag} },
	})

	fmt.Println(">> Use thread with blank memory")

	ctx := &agent.RunContext{Thread: ag.NewThread()}

	fmt.Println(must(ag.Run(ctx, message.NewText("Hello, what is the square root of 9?"))))

	fmt.Println(must(ag.Run(ctx, message.NewText("My name is Ruaidhrí"))))

	fmt.Println(must(ag.Run(ctx, message.NewText("I am 20 years old"))))

	// We can serialize the thread. The serialized state will include the state of the memory component.
	serializedThread := must(json.Marshal(ctx.Thread))

	fmt.Println(">> Use new thread with previously created memories")

	deserializedThread := must(ag.UnmarshalThread(serializedThread))

	fmt.Println(must(ag.Run(&agent.RunContext{Thread: deserializedThread}, message.NewText("What is my name and age?"))))
}

type UserInfo struct {
	Name string
	Age  int
}

var _ memory.ContextProvider = (*UserInfoMemory)(nil)

type UserInfoMemory struct {
	UserInfo UserInfo
	Agent    *agent.Agent `json:"-"`
}

func (u *UserInfoMemory) Invoked(ctx *memory.InvokedContext) error {
	if u.UserInfo.Age != 0 && u.UserInfo.Name != "" {
		// We already have the user info.
		return nil
	}
	if !slices.ContainsFunc(ctx.Messages, func(msg *message.Message) bool { return msg.Role == message.RoleUser }) {
		// No user messages to extract info from.
		return nil
	}
	out, _, err := agent.RunFor[UserInfo](u.Agent, nil, append(ctx.Messages,
		message.NewText("Extract the user's name and age from the message if present. If not present return empty values."),
	)...)
	if err != nil {
		return err
	}
	u.UserInfo.Name = cmp.Or(u.UserInfo.Name, out.Name)
	u.UserInfo.Age = cmp.Or(u.UserInfo.Age, out.Age)
	return nil
}

func (u *UserInfoMemory) Invoking(ctx *memory.InvokingContext) (*memory.Context, error) {
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
