// Copyright (c) Microsoft. All rights reserved.

package demo

import (
	"context"
	"fmt"
	"iter"
	"log/slog"
	"os"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

// ANSI color codes.
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

// Logger prints friendly demo output and can also be used as a slog handler.
type Logger struct {
	n      int
	attrs  []slog.Attr
	groups []string
}

func NewLogger(name, description string, metadata ...string) *Logger {
	var kvs []kv
	for i := 0; i < len(metadata)-1; i += 2 {
		kvs = append(kvs, kv{key: metadata[i], value: metadata[i+1]})
	}
	welcome(name, description, kvs)
	return &Logger{}
}

func (mw *Logger) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		mw.n++
		fmt.Printf("%s%s===== Run %d =====%s\n\n", colorYellow, colorBold, mw.n, colorReset)
		for _, msg := range slices.Backward(messages) {
			if msg.Role == message.RoleUser {
				user(msg.String())
				break
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
		if v, _ := agent.GetOption(opts, agent.Stream); v {
			fmt.Printf("\n\n")
		}
	}
}

// Enabled reports whether records at level should be logged.
func (mw *Logger) Enabled(context.Context, slog.Level) bool { return true }

// Handle writes a slog record using the demo output style.
func (mw *Logger) Handle(_ context.Context, record slog.Record) error {
	printf("%s%s%s:%s %s", colorGray, colorBold, record.Level, colorReset, record.Message)
	prefix := mw.attrPrefix()
	for _, attr := range mw.attrs {
		printAttr(prefix, attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		printAttr(prefix, attr)
		return true
	})
	fmt.Printf("\n\n")
	return nil
}

// WithAttrs returns a handler with attrs attached to each record.
func (mw *Logger) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *mw
	clone.attrs = append(slices.Clone(mw.attrs), attrs...)
	return &clone
}

// WithGroup returns a handler with name applied to subsequent attrs.
func (mw *Logger) WithGroup(name string) slog.Handler {
	if name == "" {
		return mw
	}
	clone := *mw
	clone.groups = append(slices.Clone(mw.groups), name)
	return &clone
}

func (mw *Logger) attrPrefix() string {
	if len(mw.groups) == 0 {
		return ""
	}
	return strings.Join(mw.groups, ".") + "."
}

func printAttr(prefix string, attr slog.Attr) {
	attr.Value = attr.Value.Resolve()
	if attr.Equal(slog.Attr{}) {
		return
	}
	if attr.Value.Kind() == slog.KindGroup {
		groupPrefix := prefix
		if attr.Key != "" {
			groupPrefix += attr.Key + "."
		}
		for _, groupAttr := range attr.Value.Group() {
			printAttr(groupPrefix, groupAttr)
		}
		return
	}
	fmt.Printf(" %s%s%s=%s%v", colorGray, prefix, attr.Key, colorReset, attr.Value.Any())
}

func welcome(name, description string, kvs []kv) {
	size := len(name) + 7 + 7
	fmt.Printf("%s%s", colorCyan, colorBold)
	fmt.Printf("╔%s╗\n", strings.Repeat("═", size))
	fmt.Printf("║%s%s%s║\n", strings.Repeat(" ", 7), name, strings.Repeat(" ", 7))
	fmt.Printf("╚%s╝\n", strings.Repeat("═", size))
	fmt.Printf("%s\n", colorReset)
	for _, kv := range kvs {
		fmt.Printf("%s%s:%s %s%s%s\n", colorGray, kv.key, colorReset, colorMagenta, kv.value, colorReset)
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
	os.Exit(1)
}

func Panicf(format string, args ...any) {
	printf("%s%s⚠️ %s %s\n",
		colorYellow, colorBold, colorReset,
		fmt.Sprintf(format, args...))
	os.Exit(1)
}

func UserInputRequest(req *message.FunctionApprovalRequestContent) bool {
	assistant()
	printf("%s%s🔔 Approval Request:%s\n",
		colorYellow, colorBold, colorReset)
	fmt.Printf("   The agent wants to call: %s%s%s\n", colorMagenta, req.FunctionCall.Name, colorReset)
	fmt.Printf("   With arguments: %s%s%s\n\n", colorGray, req.FunctionCall.Arguments, colorReset)
	fmt.Printf("Please reply Y to approve.\n\n")

	var approval string
	_, _ = fmt.Scanln(&approval)
	fmt.Printf("\n")
	return approval == "Y" || approval == "y"
}

func assistant() {
	printf("%s%sAssistant:%s ", colorBlue, colorBold, colorReset)
}

func printf(format string, args ...any) {
	// now := time.Now().Format("15:04:05")
	// fmt.Printf("%s[%s]%s ", colorGray, now, colorReset)
	fmt.Printf(format, args...)
}

func printerr(err any) {
	printf("%s❌ Error: %v%s\n\n", colorRed, err, colorReset)
}

func AzureTokenCredential() *azidentity.DefaultAzureCredential {
	if os.Getenv("AZURE_OPENAI_ENDPOINT") == "" {
		Panic("AZURE_OPENAI_ENDPOINT environment variable is not set.")
	}
	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		Panicf("failed to create Azure default credential: %v", err)
	}
	return token
}
