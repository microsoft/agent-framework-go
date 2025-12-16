// Copyright (c) Microsoft. All rights reserved.

package demo

import (
	"context"
	"fmt"
	"iter"
	"strings"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/message"
)

// ANSI color codes
const (
	colorReset   = "\033[0m"
	colorRed     = "\033[31m"
	colorGreen   = "\033[32m"
	colorYellow  = "\033[33m"
	colorBlue    = "\033[34m"
	colorMagenta = "\033[35m"
	colorCyan    = "\033[36m"
	colorGray    = "\033[90m"
	colorBold    = "\033[1m"
)

type kv struct {
	key, value string
}

type logger struct {
	n int
}

func NewLogger(name, description string, metadata ...string) middleware.Middleware {
	var kvs []kv
	for i := 0; i < len(metadata)-1; i += 2 {
		kvs = append(kvs, kv{key: metadata[i], value: metadata[i+1]})
	}
	welcome(name, description, kvs)
	return &logger{}
}

func (mw *logger) Run(ctx context.Context, next middleware.RunFunc, messages []*message.Message, opts ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		mw.n++
		fmt.Printf("===== Run %d =====\n\n", mw.n)
		for _, msg := range messages {
			if msg.Role == message.RoleUser {
				user(msg.String())
			}
		}
		first := true
		for update, err := range next(ctx, messages, opts...) {
			if err == nil && update.String() != "" {
				if first {
					assistant()
					first = false
				}
			}
			if !yield(update, err) {
				break
			}
		}
		if v, _ := agentopt.Get(opts, agentopt.Stream); v {
			fmt.Printf("\n\n")
		}
	}
}

func welcome(name, description string, kvs []kv) {
	size := len(name) + 7 + 7
	fmt.Printf("%s%s", colorCyan, colorBold)
	fmt.Printf("╔%s╗\n", strings.Repeat("═", size))
	fmt.Printf("║%s%s%s║\n", strings.Repeat(" ", 7), name, strings.Repeat(" ", 7))
	fmt.Printf("╚%s╝\n", strings.Repeat("═", size))
	fmt.Printf("%s\n", colorReset)
	for _, kv := range kvs {
		fmt.Printf("%s%s:%s %s%s%s\n", colorGray, kv.key, colorReset, colorGreen, kv.value, colorReset)
	}
	fmt.Printf("%s%s%s\n", colorGray, description, colorReset)
	fmt.Printf("\n")
	fmt.Printf("%s%s%s\n\n", colorGray, strings.Repeat("─", 60), colorReset)
}

func user(query string) {
	if query == "" {
		return
	}
	printf("%s%sYou:%s %s\n",
		colorGreen, colorBold, colorReset, query)
}

func Response(resp fmt.Stringer, err error) {
	if err != nil {
		printerr(err)
		return
	}
	txt := resp.String()
	if txt != "" {
		fmt.Print(resp)
		if _, ok := resp.(*message.ResponseUpdate); !ok {
			fmt.Print("\n\n")
		}
	}
}

func Assistant(msg ...any) {
	if len(msg) == 0 {
		return
	}
	assistant()
	fmt.Println(msg...)
}

func Assistantf(format string, args ...any) {
	printf("%s%sAssistant:%s %s\n",
		colorBlue, colorBold, colorReset,
		fmt.Sprintf(format, args...))
}

func Panic(msg any) {
	printf("%s%s⚠️ %s %s\n",
		colorYellow, colorBold, colorReset,
		msg)
}

func Panicf(format string, args ...any) {
	printf("%s%s⚠️ %s %s\n",
		colorYellow, colorBold, colorReset,
		fmt.Sprintf(format, args...))
}

func UserInputRequest(req *message.FunctionApprovalRequestContent) bool {
	assistant()
	printf("%s%s🔔 Approval Request:%s\n",
		colorYellow, colorBold, colorReset)
	fmt.Printf("   The agent wants to call: %s%s%s\n", colorMagenta, req.FunctionCall.Name, colorReset)
	fmt.Printf("   With arguments: %s%s%s\n\n", colorGray, req.FunctionCall.Arguments, colorReset)
	fmt.Printf("Please reply Y to approve.\n\n")

	var approval string
	fmt.Scanln(&approval)
	fmt.Printf("\n")
	return approval == "Y"
}

func assistant() {
	printf("%s%sAssistant:%s ", colorBlue, colorBold, colorReset)
}

func printf(format string, args ...any) {
	//now := time.Now().Format("15:04:05")
	//fmt.Printf("%s[%s]%s ", colorGray, now, colorReset)
	fmt.Printf(format, args...)
}

func printerr(err any) {
	printf("%s❌ Error: %v%s\n\n", colorRed, err, colorReset)
}
