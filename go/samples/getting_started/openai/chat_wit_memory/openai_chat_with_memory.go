package main

import (
	"context"
	"fmt"
	"slices"

	"github.com/microsoft/agent-framework/go/agent"
	"github.com/microsoft/agent-framework/go/message"
	"github.com/microsoft/agent-framework/go/openai"
)

func main() {
	ag := openai.NewChatAgent(openai.AgentConfig{
		Model:              "gpt-4o-mini",
		SystemInstructions: "You are a friendly assistant. Always address the user by their name.",
		ContextProvider:    &UserInfoMemory{},
	})

	thread := ag.NewThread()

	resp, err := ag.Run(context.Background(), thread, nil, message.NewText("Hello, what is the square root of 9?"))
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Text())

	resp, err = ag.Run(context.Background(), thread, nil, message.NewText("My name is Ruaidhrí"))
	if err != nil {
		panic(err)
	}
	fmt.Println(resp.Text())
}

type UserInfo struct {
	Name string
}

var _ agent.ContextProvider = (*UserInfoMemory)(nil)

type UserInfoMemory struct {
	UserInfo *UserInfo
	Agent    *agent.Agent
}

func (u *UserInfoMemory) Invoked(ctx context.Context, messages []*message.Message, responses []*message.Message, _ error) error {
	if u.UserInfo != nil || u.Agent == nil {
		return nil
	}
	hasUserMsg := slices.ContainsFunc(messages, func(msg *message.Message) bool {
		return msg.Role == message.RoleUser
	})
	if !hasUserMsg {
		return nil
	}
	ret, err := u.Agent.Run(ctx, nil, nil, append(messages,
		message.NewText("Extract the user's name from the conversation. Respond with just the name. Return an empty response if the name is not present."),
	)...)
	if err != nil {
		return err
	}
	if name := ret.Text(); name != "" {
		u.UserInfo = &UserInfo{Name: name}
	}
	return nil
}

func (u *UserInfoMemory) Invoking(ctx context.Context, messages []*message.Message) (*agent.Context, error) {
	var instructions string
	if u.UserInfo == nil {
		instructions = "Ask the user for their name and politely decline to answer any questions until they provide it."
	} else {
		instructions = "The user's name is " + u.UserInfo.Name + "."
	}
	return &agent.Context{
		Instructions: instructions,
	}, nil
}
