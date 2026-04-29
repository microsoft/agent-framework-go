// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bufio"
	"cmp"
	"context"
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

		err := runWithApprovals(context.Background(), a, session, message.NewText(input))
		if err != nil {
			log.Fatal(err)
		}
	}
}

func runWithApprovals(ctx context.Context, a *agent.Agent, session agent.Session, input *message.Message) error {
	current := input
	for {
		resp, err := a.RunMessage(ctx, current, agent.WithSession(session)).Collect()
		if err != nil {
			return err
		}
		if text := strings.TrimSpace(resp.String()); text != "" {
			fmt.Println(text)
		}

		responses := []message.Content{}
		for c := range resp.Contents() {
			switch v := c.(type) {
			case *message.FunctionApprovalRequestContent:
				approved := askApproval(v)
				responses = append(responses, v.Response(approved))
			case *message.FunctionCallContent:
				if !strings.EqualFold(v.Name, "request_approval") {
					continue
				}
				approved := askToolApproval(v)
				responses = append(responses, &message.FunctionResultContent{CallID: v.CallID, Result: map[string]any{"approved": approved}})
			}
		}
		if len(responses) == 0 {
			return nil
		}
		current = message.New(responses...)
	}
}

func askApproval(req *message.FunctionApprovalRequestContent) bool {
	call := req.FunctionCall
	if call == nil {
		fmt.Print("Approve this action? (y/n): ")
	} else {
		fmt.Printf("Approve tool call %q with args %s? (y/n): ", call.Name, call.Arguments)
	}
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}

func askToolApproval(call *message.FunctionCallContent) bool {
	prettyArgs := call.Arguments
	var parsed any
	if err := json.Unmarshal([]byte(call.Arguments), &parsed); err == nil {
		if b, err := json.Marshal(parsed); err == nil {
			prettyArgs = string(b)
		}
	}
	fmt.Printf("Approve tool call %q with args %s? (y/n): ", call.Name, prettyArgs)
	reader := bufio.NewReader(os.Stdin)
	line, _ := reader.ReadString('\n')
	line = strings.TrimSpace(strings.ToLower(line))
	return line == "y" || line == "yes"
}
