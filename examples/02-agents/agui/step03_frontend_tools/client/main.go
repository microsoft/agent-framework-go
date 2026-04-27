// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bufio"
	"cmp"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	aguiSSEClient "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/client/sse"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/aguiagent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/microsoft/agent-framework-go/tool/functool"
)

var serverURL = cmp.Or(os.Getenv("AGUI_SERVER_URL"), "http://localhost:8888")

func main() {
	frontendTool := functool.MustNew(functool.Config{
		Name:        "get_user_location",
		Description: "Get the user's current location from GPS.",
	}, func(ctx tool.Context, in struct{}) (string, error) {
		return "Amsterdam, Netherlands (52.37°N, 4.90°E)", nil
	})

	a := aguiagent.New(
		aguiSSEClient.NewClient(aguiSSEClient.Config{Endpoint: serverURL}),
		aguiagent.Config{
			Config: agent.Config{
				Tools: []tool.Tool{frontendTool},
			},
		},
	)

	session, err := a.CreateSession(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\nUser (:q to quit): ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				log.Fatal(err)
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

		for update, err := range a.RunText(context.Background(), input, agent.WithSession(session), agent.Stream(true)) {
			if err != nil {
				log.Fatal(err)
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
