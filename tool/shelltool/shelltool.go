// Copyright (c) Microsoft. All rights reserved.

// Package shelltool provides a shell command execution tool that can be
// registered with an agent. It mirrors the .NET LocalShellTool / LocalShellExecutor
// design: approval-in-the-loop is the default security boundary; an allow/deny
// [Policy] offers a best-effort pre-execution guardrail.
//
// # Security
//
// Running agent-generated shell commands is inherently dangerous.  This package
// provides two complementary controls:
//
//   - [Policy]: an allow/deny list of regular expressions checked before
//     commands reach the shell. The policy is a UX guardrail, NOT a security
//     boundary — a determined model can trivially work around regex checks.
//
//   - Approval-in-the-loop: [NewLocal] returns a [Local] that reports approval
//     is required through [tool.ApprovalRequiredTool], so the harness
//     tool-approval middleware prompts a human before every execution. This is
//     the primary security control. Pass [LocalConfig.AcknowledgeUnsafe] = true
//     only when you have an independent isolation mechanism (e.g. a Docker
//     container) and understand the risk.
//
// # Usage
//
//	t, err := shelltool.NewLocal(shelltool.LocalConfig{})
//	if err != nil {
//		// handle invalid configuration
//	}
//	env := shelltool.NewEnvironmentProvider(t, shelltool.EnvironmentProviderConfig{})
//	cfg := agent.Config{
//		Tools:            []tool.Tool{t},
//		ContextProviders: []agent.ContextProvider{env},
//	}
package shelltool

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Mode controls whether each call spawns a fresh shell or reuses a single
// long-lived shell process.
type Mode int

const (
	// ModePersistent keeps a single shell alive across calls so that
	// directory changes, exported variables, and history persist. A
	// persistent executor MUST NOT be shared across concurrent users or
	// agent sessions.
	ModePersistent Mode = iota
	// ModeStateless spawns a new shell process for every command. Safe to
	// share across concurrent calls; no state leaks between invocations.
	ModeStateless
)

// Result is the outcome of a single shell command invocation.
type Result struct {
	// Stdout is the captured standard output, possibly truncated.
	Stdout string
	// Stderr is the captured standard error, possibly truncated.
	Stderr string
	// ExitCode is the exit status reported by the process. -1 if the process
	// did not exit cleanly.
	ExitCode int
	// Duration is how long the command ran end-to-end.
	Duration time.Duration
	// Truncated is true when stdout or stderr was truncated.
	Truncated bool
	// TimedOut is true when the command was killed for exceeding the timeout.
	TimedOut bool
}

// FormatForModel returns a single text block combining stdout, stderr, status
// flags, and the exit code — suitable for returning to the language model.
func (r Result) FormatForModel() string {
	var sb strings.Builder
	if r.Stdout != "" {
		sb.WriteString(r.Stdout)
		if r.Truncated {
			sb.WriteString("\n[stdout truncated]")
		}
		sb.WriteByte('\n')
	}
	if r.Stderr != "" {
		sb.WriteString("stderr: ")
		sb.WriteString(r.Stderr)
		sb.WriteByte('\n')
	}
	if r.TimedOut {
		sb.WriteString("[command timed out]\n")
	}
	sb.WriteString("exit_code: ")
	fmt.Fprint(&sb, r.ExitCode)
	return sb.String()
}

// ShellRequest is a shell command awaiting a policy decision.
type ShellRequest struct {
	// Command is the full command line that the agent wants to run.
	Command string

	// WorkingDirectory is the optional working directory the command will
	// execute in, if known.
	WorkingDirectory string
}

// Policy is a layered allow/deny pattern filter for shell commands.
//
// The regex filter is a UX guardrail, NOT a security boundary. It is intended
// to fast-fail commands operators would rather reject before execution while
// the primary isolation is approval-in-the-loop or container sandboxing.
//
// A policy constructed with no patterns allows any non-empty command. Deny
// patterns are checked first. If an allow list is supplied, it is exclusive:
// any command that matches none of the allow patterns is denied. Supplying an
// empty allow list therefore denies every command, while leaving it nil
// disables allow-list checks entirely. A custom callback runs last and may
// override the default allow; it cannot re-enable a command already rejected
// by the deny list or allow list.
type Policy struct {
	denies []policyPattern
	allows []policyPattern
	custom func(ShellRequest) (allowed bool, reason string, ok bool)
}

// PolicyConfig configures a [Policy].
type PolicyConfig struct {
	// DenyList contains patterns that trigger a deny outcome. Nil or empty
	// disables the deny list.
	DenyList []string

	// AllowList contains optional allow-list patterns. When nil, allow-list
	// checks are disabled. When supplied, including as an empty slice, any
	// command that matches none of the patterns is denied.
	AllowList []string

	// Custom optionally overrides the default allow after the deny list and
	// allow list pass. Returning ok=false leaves the default allow in place.
	// This callback cannot re-enable a command already rejected earlier in
	// evaluation.
	Custom func(ShellRequest) (allowed bool, reason string, ok bool)
}

type policyPattern struct {
	pattern string
	re      *regexp.Regexp
}

// NewPolicy creates a [Policy] from cfg. Patterns are matched case-insensitively.
func NewPolicy(cfg PolicyConfig) (*Policy, error) {
	denies, err := compilePolicyPatterns("deny", cfg.DenyList)
	if err != nil {
		return nil, err
	}
	var allows []policyPattern
	if cfg.AllowList != nil {
		allows, err = compilePolicyPatterns("allow", cfg.AllowList)
		if err != nil {
			return nil, err
		}
	}
	return &Policy{denies: denies, allows: allows, custom: cfg.Custom}, nil
}

func compilePolicyPatterns(kind string, patterns []string) ([]policyPattern, error) {
	compiled := make([]policyPattern, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := regexp.Compile("(?i:" + pattern + ")")
		if err != nil {
			return nil, fmt.Errorf("shelltool: invalid %s pattern %q: %w", kind, pattern, err)
		}
		compiled = append(compiled, policyPattern{pattern: pattern, re: re})
	}
	return compiled, nil
}

// Evaluate returns whether request may run and a human-readable reason when
// one applies. Evaluation order is: empty-command guard, deny patterns,
// allow-list denial when supplied and unmatched, custom override, default
// allow.
func (p *Policy) Evaluate(request ShellRequest) (allowed bool, reason string) {
	if p == nil {
		return true, ""
	}
	command := strings.TrimSpace(request.Command)
	if command == "" {
		return false, "empty command"
	}
	for _, deny := range p.denies {
		if deny.re.MatchString(command) {
			return false, "matched deny pattern: " + deny.pattern
		}
	}
	if p.allows != nil {
		matched := false
		for _, allow := range p.allows {
			if allow.re.MatchString(command) {
				matched = true
				break
			}
		}
		if !matched {
			return false, "command does not match allow list"
		}
	}
	if p.custom != nil {
		if allowed, reason, ok := p.custom(request); ok {
			return allowed, reason
		}
	}
	return true, ""
}
