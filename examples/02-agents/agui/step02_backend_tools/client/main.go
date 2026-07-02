// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"os"
	"strings"

	aguiSSEClient "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/client/sse"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/aguiprovider"
)

var (
	serverURL = cmp.Or(os.Getenv("AGUI_SERVER_URL"), "http://localhost:8888")
	_         = demo.NewLogger(
		"AG-UI Backend Tools Client",
		"Connects to an AG-UI server over SSE. Start the matching server first.",
		"Server", serverURL,
	)
)

func main() {
	a := aguiprovider.NewAgent(
		aguiSSEClient.NewClient(aguiSSEClient.Config{Endpoint: serverURL}),
		aguiprovider.AgentConfig{},
	)

	session, err := a.CreateSession(context.Background())
	if err != nil {
		demo.Panicf("failed to connect to AG-UI server at %s: %v", serverURL, err)
	}

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\nUser (:q to quit): ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				demo.Panicf("failed to read input: %v", err)
			}
			return
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == ":q" || strings.EqualFold(input, "quit") || strings.EqualFold(input, "exit") {
			return
		}

		fmt.Print("Assistant: ")
		for update, err := range a.RunText(context.Background(), input, agent.WithSession(session), agent.Stream(true)) {
			if err != nil {
				demo.Panicf("agent run failed: %v", err)
			}
			for _, c := range update.Contents {
				if text, ok := c.(*message.TextContent); ok {
					fmt.Print(text.Text)
				}
			}
		}
		fmt.Println()
	}
}
