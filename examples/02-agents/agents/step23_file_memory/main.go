// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	"fmt"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/harness/filememory"
	"github.com/microsoft/agent-framework-go/agent/harness/filestore"
	"github.com/microsoft/agent-framework-go/tool"
)

func main() {
	ctx := context.Background()
	session := &agent.Session{}
	provider := filememory.New(
		filestore.NewInMemoryStore(),
		func(*agent.Session) filememory.State { return filememory.State{WorkingFolder: "demo"} },
		nil,
	)

	_, opts, err := provider.Invoking(ctx, agent.InvokingContext{Options: []agent.Option{agent.WithSession(session)}})
	if err != nil {
		panic(err)
	}

	write := requireTool(opts, filememory.WriteToolName)
	read := requireTool(opts, filememory.ReadToolName)
	ls := requireTool(opts, filememory.LsToolName)

	if _, err := write.Call(ctx, `{"file_name":"plan.md","content":"1. Inspect recent changes\n2. Implement the selected port\n","description":"Current implementation plan"}`); err != nil {
		panic(err)
	}
	contents, err := read.Call(ctx, `{"file_name":"plan.md"}`)
	if err != nil {
		panic(err)
	}
	fmt.Println(contents)

	files, err := ls.Call(ctx, `{}`)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%#v\n", files)
}

func requireTool(opts []agent.Option, name string) tool.FuncTool {
	for _, opt := range opts {
		if tl, ok := opt.Value().(tool.FuncTool); ok && tl.Name() == name {
			return tl
		}
	}
	panic("tool not found: " + name)
}
