// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bufio"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	aguiSSEClient "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/client/sse"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/provider/aguiagent"
	"github.com/microsoft/agent-framework-go/message"
)

var serverURL = cmp.Or(os.Getenv("AGUI_SERVER_URL"), "http://localhost:8888")

func main() {
	a := aguiagent.New(
		aguiSSEClient.NewClient(aguiSSEClient.Config{Endpoint: serverURL}),
		aguiagent.Config{},
	)

	session, err := a.CreateSession(context.Background())
	if err != nil {
		log.Fatal(err)
	}

	state := any(map[string]any{})
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\nUser (:q to quit, :state to show state): ")
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
		if strings.EqualFold(input, ":state") {
			printState(state)
			continue
		}

		msg := message.New(&message.TextContent{Text: input}, toStateContent(state))
		resp, err := a.RunMessage(context.Background(), msg, agent.WithSession(session)).Collect()
		if err != nil {
			log.Fatal(err)
		}
		if text := strings.TrimSpace(resp.String()); text != "" {
			fmt.Println(text)
		}
		if nextState, ok := extractState(resp); ok {
			state = nextState
			printState(state)
		}
	}
}

func toStateContent(state any) *message.DataContent {
	b, _ := json.Marshal(state)
	return &message.DataContent{MediaType: "application/json", Data: base64.StdEncoding.EncodeToString(b)}
}

func extractState(resp *message.Response) (any, bool) {
	for c := range resp.Contents() {
		dc, ok := c.(*message.DataContent)
		if !ok || strings.ToLower(strings.TrimSpace(dc.MediaType)) != "application/json" {
			continue
		}
		b, err := dc.Bytes()
		if err != nil {
			continue
		}
		var state any
		if err := json.Unmarshal(b, &state); err != nil {
			continue
		}
		return state, true
	}
	return nil, false
}

func printState(state any) {
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		fmt.Printf("state: %+v\n", state)
		return
	}
	fmt.Printf("\nCurrent state:\n%s\n", string(b))
}
