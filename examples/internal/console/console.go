// Copyright (c) Microsoft. All rights reserved.

package console

import (
	"fmt"
	"strings"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
)

// ANSI color codes
const (
	colorReset   = "\033[0m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorCyan    = "\033[36m"
	colorGray    = "\033[90m"
	colorBold    = "\033[1m"
)

func getTimestamp() string {
	return time.Now().Format("15:04:05")
}

func Welcome(name, description, model string) {
	size := len(name) + 7 + 7
	fmt.Printf("%s%s", colorCyan, colorBold)
	fmt.Printf("╔%s╗\n", strings.Repeat("═", size))
	fmt.Printf("║%s%s%s║\n", strings.Repeat(" ", 7), name, strings.Repeat(" ", 7))
	fmt.Printf("╚%s╝\n", strings.Repeat("═", size))
	fmt.Printf("%s\n", colorReset)
	fmt.Printf("%sModel: %s%s%s\n", colorGray, colorMagenta, model, colorReset)
	fmt.Printf("%s%s%s\n", colorGray, description, colorReset)
	fmt.Printf("\n")
	fmt.Printf("%s%s%s\n", colorGray, strings.Repeat("─", 60), colorReset)

}

func User(query string) {
	fmt.Printf("\n%s[%s]%s %s%sYou:%s %s\n",
		colorGray, getTimestamp(), colorReset,
		colorGreen, colorBold, colorReset, query)
}

func Agent() {
	fmt.Printf("\n%s[%s]%s %s%sAgent:%s ",
		colorGray, getTimestamp(), colorReset,
		colorBlue, colorBold, colorReset)
}

func AgentResponse(resp *agent.RunResponse, err error) {
	Agent()
	if err != nil {
		fmt.Printf("\n%s❌ Error: %v%s\n", colorRed, err, colorReset)
		return
	}
	fmt.Printf("%s\n", resp)
}

func AgentStreamResponse(update *agent.RunResponseUpdate, err error) {
	if err != nil {
		fmt.Printf("\n%s❌ Error: %v%s\n", colorRed, err, colorReset)
		return
	}
	fmt.Print(update)
}
