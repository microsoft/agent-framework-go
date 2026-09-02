// Copyright (c) Microsoft. All rights reserved.

package toolautocall

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/otelx"
	"github.com/microsoft/agent-framework-go/internal/slogx"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

const (
	defaultMaximumConsecutiveErrorsPerRequest = 3
	defaultMaximumIterationsPerRequest        = 40
	opExecuteTool                             = "execute_tool"
	pendingApprovalRequestsStateKey           = "toolautocall.pendingApprovalRequests"
	pendingAutoApprovedCallsStateKey          = "toolautocall.pendingAutoApprovedCalls"

	attrKeyOperationName = "gen_ai.operation.name"
	attrKeyToolName      = "gen_ai.tool.name"
	attrKeyToolCallID    = "gen_ai.tool.call.id"
	attrKeyToolType      = "gen_ai.tool.type"
	attrKeyToolDesc      = "gen_ai.tool.description"
	attrKeyErrorType     = "error.type"
)

// Config configures the automatic tool invocation middleware.
//
// When a provider response contains function call content, the middleware looks
// up the matching tool, invokes it, sends the generated function result back to
// the provider, and repeats this loop until there are no more function calls or
// another stop condition is met. To disable the middleware, set
// MaximumIterationsPerRequest to new(0). A disabled middleware delegates once
// without changing the request options, messages, or response updates.
type Config struct {
	// Logger receives diagnostic events about function invocation.
	// If nil, no events are logged.
	Logger *slog.Logger

	// LogSensitiveData controls whether sensitive function arguments and results
	// are included in logs. The default is false.
	LogSensitiveData bool

	// AdditionalTools are tools the middleware can invoke without adding them to
	// provider requests. Request tools supplied through agent options take
	// precedence; this collection is consulted afterward, which is useful when the
	// provider is already configured with tool declarations out of band.
	AdditionalTools []tool.Tool

	// IncludeDetailedErrors controls whether tool error details are included in
	// the function result sent back to the provider. When false, the provider sees
	// a generic error message while the raw error remains available on the
	// FunctionResultContent and is rethrown if the consecutive-error limit is
	// exceeded. The default is false.
	IncludeDetailedErrors bool

	// TerminateOnUnknownCalls controls whether a request to call an unknown tool
	// stops the function-calling loop. When false, the middleware returns a
	// generated "not found" function result to the provider. When true, the
	// original response is returned so the caller can handle the tool call. A known
	// schema tool that is not invocable always stops the loop. The default is false.
	TerminateOnUnknownCalls bool

	// AllowConcurrentInvocations controls whether multiple function calls from the
	// same provider response may execute in parallel. When false, function calls are
	// processed serially. The default is false.
	AllowConcurrentInvocations bool

	// MaximumConsecutiveErrorsPerRequest is the number of consecutive failing
	// function-invocation iterations allowed before the middleware returns the
	// aggregated invocation errors to the caller. A successful iteration resets the
	// count. When nil, the default of 3 is used. Set to new(0) to fail on the
	// first tool error.
	MaximumConsecutiveErrorsPerRequest *int

	// MaximumIterationsPerRequest is the maximum number of tool-invocation rounds
	// performed for a single run. When the limit is reached, the middleware makes a
	// final provider request without schema tools so no more local function calls
	// are requested. When nil, the default of 40 is used. Zero disables the
	// middleware as described by [Config]. Negative values are invalid.
	MaximumIterationsPerRequest *int

	// NewID generates synthetic IDs for generated tool-result messages and
	// approval-related fallback messages. If nil, a canonical dashed UUID is used.
	NewID func() string

	// DisableApprovalResponseBinding disables session-backed validation that
	// binds approval responses to approval requests surfaced by the framework.
	// Binding is enabled by default.
	DisableApprovalResponseBinding bool

	// DisableApprovalNotRequiredFunctionBypassing disables hiding and
	// automatically approving safe function calls returned alongside calls that
	// require approval. Bypassing is enabled by default.
	DisableApprovalNotRequiredFunctionBypassing bool
}

type autocall struct {
	logger                              slogx.Logger
	additionalTools                     []tool.Tool
	includeDetailedErrors               bool
	terminateOnUnknownCalls             bool
	allowConcurrentInvocations          bool
	maximumConsecutiveErrorsPerRequest  int
	maximumIterationsPerRequest         int
	newID                               func() string
	disableApprovalResponseBinding      bool
	disableApprovalNotRequiredBypassing bool
}

// New creates a new function-invoking chat client that wraps the provided client.
func New(cfg Config) agent.Middleware {
	if cfg.NewID == nil {
		cfg.NewID = uuid.NewString
	}
	maximumConsecutiveErrorsPerRequest := defaultMaximumConsecutiveErrorsPerRequest
	if cfg.MaximumConsecutiveErrorsPerRequest != nil {
		maximumConsecutiveErrorsPerRequest = *cfg.MaximumConsecutiveErrorsPerRequest
	}
	maximumIterationsPerRequest := defaultMaximumIterationsPerRequest
	if cfg.MaximumIterationsPerRequest != nil {
		maximumIterationsPerRequest = *cfg.MaximumIterationsPerRequest
	}
	ac := &autocall{
		logger: slogx.Logger{
			Logger:        cfg.Logger,
			SensitiveData: cfg.LogSensitiveData,
			Type:          slogx.TypeMiddleware,
			Name:          "autocall",
		},
		additionalTools:                     cfg.AdditionalTools,
		includeDetailedErrors:               cfg.IncludeDetailedErrors,
		terminateOnUnknownCalls:             cfg.TerminateOnUnknownCalls,
		allowConcurrentInvocations:          cfg.AllowConcurrentInvocations,
		maximumConsecutiveErrorsPerRequest:  maximumConsecutiveErrorsPerRequest,
		maximumIterationsPerRequest:         maximumIterationsPerRequest,
		newID:                               cfg.NewID,
		disableApprovalResponseBinding:      cfg.DisableApprovalResponseBinding,
		disableApprovalNotRequiredBypassing: cfg.DisableApprovalNotRequiredFunctionBypassing,
	}
	return ac
}

func (f *autocall) Run(next agent.RunFunc, ctx context.Context, messages []*message.Message, opts ...agent.Option) iter.Seq2[*agent.ResponseUpdate, error] {
	return func(yield func(*agent.ResponseUpdate, error) bool) {
		if f.maximumConsecutiveErrorsPerRequest < 0 {
			yield(nil, fmt.Errorf("toolautocall: MaximumConsecutiveErrorsPerRequest must be 0 or greater, got %d", f.maximumConsecutiveErrorsPerRequest))
			return
		}
		if f.maximumIterationsPerRequest < 0 {
			yield(nil, fmt.Errorf("toolautocall: MaximumIterationsPerRequest must be 0 or greater, got %d", f.maximumIterationsPerRequest))
			return
		}
		if f.maximumIterationsPerRequest == 0 {
			for update, err := range next(ctx, messages, opts...) {
				if !yield(update, err) || err != nil {
					return
				}
			}
			return
		}
		var messagesCloned bool
		session, _ := agent.GetOption(opts, agent.WithSession)
		yieldUpdate := func(update *agent.ResponseUpdate) bool {
			if !f.disableApprovalResponseBinding && update != nil {
				if err := recordPendingApprovalRequests(session, update.Contents); err != nil {
					yield(nil, err)
					return false
				}
			}
			return yield(update, nil)
		}
		if !f.disableApprovalResponseBinding {
			var changed bool
			var err error
			messages, changed, err = bindApprovalResponses(session, messages)
			if err != nil {
				yield(nil, err)
				return
			}
			messagesCloned = changed
		}
		if !f.disableApprovalNotRequiredBypassing {
			var changed bool
			var err error
			messages, changed, err = injectPendingAutoApprovedCalls(session, messages)
			if err != nil {
				yield(nil, err)
				return
			}
			messagesCloned = messagesCloned || changed
		}
		tools, _ := f.createToolsMap(agent.AllOptions(opts, agent.WithTool))

		// This is a synthetic ID since we're generating the tool messages instead of getting them from
		// the underlying provider. When emitting the streamed chunks, it's perfectly valid for us to
		// use the same message ID for all of them within a given iteration, as this is a single logical
		// message with multiple content items. We could also use different message IDs per tool content,
		// but there's no benefit to doing so.
		toolMsgID := f.newID()
		var errCount int
		if hasAnyApprovalContent(messages) {
			messages = slices.Clone(messages)
			messagesCloned = true
			// We also need a synthetic ID for the function call content for approved function calls
			// where we don't know what the original message id of the function call was.
			funcCallFallbackMsgID := f.newID()

			// A previous turn may have translated FunctionCallContents from the inner client into approval requests sent back to the caller,
			// for any functions that were actually ApprovalRequiredFunctions. If the incoming chat messages include responses to those
			// approval requests, we need to process them now. This entails removing these manufactured approval requests from the chat message
			// list and replacing them with the appropriate FunctionCallContents and FunctionResultContents that would have been generated if
			// the inner client had returned them directly.
			var notInvokedMsgs []toolApprovalResultWithRequestMessage
			var preDownstreamCallHistory []*message.Message
			var err error
			messages, preDownstreamCallHistory, notInvokedMsgs, err = f.processToolApprovalResponses(ctx, messages, toolMsgID, funcCallFallbackMsgID)
			if err != nil {
				yield(nil, err)
				return
			}
			for _, msg := range preDownstreamCallHistory {
				if !yield(convertToolResultMsgToUpdate(msg, msg.ID), nil) {
					return
				}
			}
			// Invoke approved approval responses, which generates some additional FRC wrapped in ChatMessage.
			var newMsg *message.Message
			newMsg, errCount, err = f.invokeApprovedToolApprovalResponses(ctx, notInvokedMsgs, tools, 0)
			if err != nil {
				yield(nil, err)
				return
			}
			if newMsg != nil {
				opts = updateOptionsForNextIteration(opts)
				messages = append(messages, newMsg)
				newMsg.ID = toolMsgID
				if !yield(convertToolResultMsgToUpdate(newMsg, toolMsgID), nil) {
					return
				}
			}
		}
		// At this point, we've fully handled all approval responses that were part of the original messages,
		// and we can now enter the main function calling loop.
		var updates []*agent.ResponseUpdate
		var functionCallContents []*message.FunctionCallContent
		for i := 0; ; i++ {
			if i >= f.maximumIterationsPerRequest {
				f.logger.Debug(ctx, "reached maximum iteration count; stopping function invocation loop", "maximumIterationsPerRequest", f.maximumIterationsPerRequest)
				opts = prepareOptionsForLastIteration(opts)
			}
			tools, requiresApproval := f.createToolsMap(agent.AllOptions(opts, agent.WithTool))

			// Reset slice without reallocating.
			updates = updates[:0]
			functionCallContents = functionCallContents[:0]
			var hasApprovalRequiringFcc bool
			var lastApprovalCheckedFCCIdx, lastYieldedUpdateIdx int
			for update, err := range next(ctx, messages, opts...) {
				if err != nil {
					yield(nil, err)
					return
				}
				if update == nil {
					yield(nil, nil)
					continue
				}
				updates = append(updates, update)
				// Accumulate function call contents from the update.
				for _, c := range update.Contents {
					if fcc, ok := c.(*message.FunctionCallContent); ok && !fcc.InformationalOnly {
						functionCallContents = append(functionCallContents, fcc)
					}
				}
				// We're streaming updates back to the caller. However, approvals requires extra handling. We should not yield any
				// FunctionCallContents back to the caller if approvals might be required, because if any actually are, we need to convert
				// all FunctionCallContents into approval requests, even those that don't require approval (we otherwise don't have a way
				// to track the FCCs to a later turn, in particular when the conversation history is managed by the service / inner client).
				// So, if there are no functions that need approval, we can yield updates with FCCs as they arrive. But if any FCC _might_
				// require approval (which just means that any Function we can possibly invoke requires approval), then we need to hold off
				// on yielding any FCCs until we know whether any of them actually require approval, which is either at the end of the stream
				// or the first time we get an FCC that requires approval. At that point, we can yield all of the updates buffered thus far
				// and anything further, replacing FCCs with approval if any required it, or yielding them as is.
				if len(functionCallContents) == 0 {
					// If there are no function calls to make yet, we can yield the update as-is.
					lastYieldedUpdateIdx++
					if !yieldUpdate(update) {
						return
					}
					continue
				}
				if !requiresApproval {
					// We have function calls but no approval-required functions. Buffer updates from this point on
					// so we can detect and suppress server-handled function calls before local invocation.
					continue
				}
				hasApprovalRequiringFcc, lastApprovalCheckedFCCIdx = checkForApprovalRequiringFCC(functionCallContents, tools, hasApprovalRequiringFcc, lastApprovalCheckedFCCIdx)
				if hasApprovalRequiringFcc {
					// If we've encountered a function call content that requires approval,
					// we need to ask for approval for all functions, since we cannot mix and match.
					// Convert all function call contents into approval requests from the last yielded update index
					// and yield all those updates.
					for ; lastYieldedUpdateIdx < len(updates); lastYieldedUpdateIdx++ {
						updateToYield := updates[lastYieldedUpdateIdx]
						updatedContent, err := f.prepareApprovalContents(ctx, updateToYield.Contents, tools, session)
						if err != nil {
							yield(nil, err)
							return
						}
						if updatedContent != nil {
							updateToYield.Contents = updatedContent
						}
						if !yieldUpdate(updateToYield) {
							return
						}
					}
					continue
				}
				// We don't have any approval requiring function calls yet, but we may receive some in future
				// so we cannot yield the updates yet. We'll just keep them in the updates list for later.
				// We will yield the updates as soon as we receive a function call content that requires approval
				// or when we reach the end of the updates stream.
			}
			// Mark function calls as informational-only if the server already provided matching function results.
			functionCallContents = markServerHandledFunctionCalls(updates, functionCallContents)

			// We need to yield any remaining updates that were not yielded while looping through the streamed updates.
			for ; lastYieldedUpdateIdx < len(updates); lastYieldedUpdateIdx++ {
				if !yieldUpdate(updates[lastYieldedUpdateIdx]) {
					return
				}
			}
			// If there's nothing more to do, break out of the loop and allow the handling at the
			// end to configure the response with aggregated data from previous requests.
			if i >= f.maximumIterationsPerRequest || hasApprovalRequiringFcc || f.shouldTerminateLoopBasedOnHandleableFunctions(ctx, functionCallContents, tools) {
				break
			}

			// We need to invoke functions.

			// Process all of the functions, adding their results into the history.
			var newMsg *message.Message
			var err error
			newMsg, errCount, err = f.processFunctionCalls(ctx, tools, functionCallContents, errCount)
			if err != nil {
				yield(nil, err)
				return
			}

			// Stream any generated function results. This mirrors what's done for ResponseAsync, where the returned messages
			// includes all activities, including generated function results.
			if !yield(convertToolResultMsgToUpdate(newMsg, toolMsgID), nil) {
				return
			}

			// Build an assistant message containing the text, reasoning, and function
			// calls that were produced this iteration. This is needed because chat APIs
			// (e.g. OpenAI) require tool result messages to be preceded by an assistant
			// message containing the corresponding tool_calls. Preserving the assistant's
			// text and reasoning content keeps the turn intact for the next provider call,
			// matching .NET's FunctionInvokingChatClient, which does
			// augmentedHistory.AddMessages(response) rather than reconstructing from the
			// function calls alone.
			processedFunctionCalls := functionCallContents[:len(newMsg.Contents)]

			// Coalesce the buffered updates for this iteration so streamed text/reasoning
			// fragments merge, then carry the text and reasoning over alongside the
			// processed (non-informational) function calls, preserving the exact
			// assistant content order the model emitted (e.g. reasoning → text →
			// tool_calls, or text following a tool_call).
			processedFCCSet := make(map[*message.FunctionCallContent]struct{}, len(processedFunctionCalls))
			for _, fcc := range processedFunctionCalls {
				processedFCCSet[fcc] = struct{}{}
			}
			var iterationContents []message.Content
			for _, u := range updates {
				iterationContents = append(iterationContents, u.Contents...)
			}
			iterationContents = message.CoalesceContents(iterationContents)
			assistantContents := make([]message.Content, 0, len(iterationContents))
			for _, c := range iterationContents {
				switch v := c.(type) {
				case *message.TextContent, *message.TextReasoningContent:
					assistantContents = append(assistantContents, c)
				case *message.FunctionCallContent:
					// Only carry over the non-informational function calls that were
					// actually processed this iteration, keeping them in their original
					// position relative to the surrounding text/reasoning.
					if _, ok := processedFCCSet[v]; ok {
						assistantContents = append(assistantContents, c)
					}
				}
			}

			// Use the augmented history as the new set of messages to send.
			// We include the original messages, the assistant message with function calls,
			// and the tool results so that the downstream provider receives a well-formed
			// conversation (user message → assistant tool_calls → tool results).
			opts = updateOptionsForNextIteration(opts)
			if !messagesCloned {
				messages = slices.Clone(messages)
				messagesCloned = true
			}
			messages = append(messages, &message.Message{
				Role:     message.RoleAssistant,
				Contents: assistantContents,
			}, newMsg)
		}
	}
}

func (f *autocall) prepareApprovalContents(ctx context.Context, contents []message.Content, tools map[string]tool.SchemaTool, session *agent.Session) ([]message.Content, error) {
	var updated []message.Content
	var autoApproved []*message.FunctionCallContent
	for i, c := range contents {
		if fcc, ok := c.(*message.FunctionCallContent); ok && !fcc.InformationalOnly {
			if updated == nil {
				updated = slices.Clone(contents)
			}
			declaration, known := tools[fcc.Name]
			_, invocable := declaration.(tool.FuncTool)
			if !f.disableApprovalNotRequiredBypassing && session != nil && known && invocable && !toolRequiresApproval(declaration) {
				autoApproved = append(autoApproved, snapshotFunctionCall(fcc))
				updated[i] = nil
				continue
			}
			f.logger.Debug(ctx, "function requires approval; converting to approval request", "funcName", fcc.Name)
			updated[i] = &message.ToolApprovalRequestContent{
				ToolCall:  fcc,
				RequestID: composeApprovalRequestID(fcc.CallID),
			}
		}
	}
	if len(autoApproved) > 0 {
		if err := recordPendingAutoApprovedCalls(session, autoApproved); err != nil {
			return nil, err
		}
		updated = slices.DeleteFunc(updated, func(content message.Content) bool { return content == nil })
	}
	return updated, nil
}

func toolRequiresApproval(t tool.SchemaTool) bool {
	approval, ok := t.(tool.ApprovalRequiredTool)
	return ok && approval.ApprovalRequired()
}

func composeApprovalRequestID(callID string) string {
	return "ficc_" + callID
}

//	checkForApprovalRequiringFCC checks if any of the provided functionCallContents require approval.
//
// Supports checking from a provided index up to the end of the list, to allow efficient incremental checking when streaming.
func checkForApprovalRequiringFCC(functionCalls []*message.FunctionCallContent, tools map[string]tool.SchemaTool, hasApprovalRequiringFcc bool, lastApprovalCheckedFCCIdx int) (bool, int) {
	if hasApprovalRequiringFcc {
		// If we already found an approval requiring FCC, we can skip checking the rest.
		return true, lastApprovalCheckedFCCIdx
	}
	for ; lastApprovalCheckedFCCIdx < len(functionCalls); lastApprovalCheckedFCCIdx++ {
		fcc := functionCalls[lastApprovalCheckedFCCIdx]
		if t, ok := tools[fcc.Name]; ok {
			if approval, ok := t.(tool.ApprovalRequiredTool); ok && approval.ApprovalRequired() {
				hasApprovalRequiringFcc = true
			}
		}
	}
	return hasApprovalRequiringFcc, lastApprovalCheckedFCCIdx
}

func convertToolResultMsgToUpdate(msg *message.Message, msgID string) *agent.ResponseUpdate {
	return &agent.ResponseUpdate{
		AdditionalProperties: msg.AdditionalProperties,
		AuthorName:           msg.AuthorName,
		Contents:             msg.Contents,
		CreatedAt:            msg.CreatedAt,
		RawRepresentation:    msg.RawRepresentation,
		Role:                 msg.Role,
		ResponseID:           msgID,
		MessageID:            msgID,
	}
}

func updateOptionsForNextIteration(opts []agent.Option) []agent.Option {
	updated := opts
	var cloned bool
	if v, ok := agent.GetOption(opts, agent.WithToolMode); ok && v.Mode() == tool.ToolModeRequired {
		// We have to reset the tool mode to be non-required after the first iteration,
		// as otherwise we'll be in an infinite loop.
		updated = slices.DeleteFunc(slices.Clone(opts), isToolModeOption)
		cloned = true
	}
	// Reset the continuation token of a background response operation
	// to signal the inner client to handle function call result rather
	// than getting the result of the operation.
	if _, ok := agent.GetOption(opts, agent.WithContinuationToken); ok {
		if !cloned {
			updated = slices.Clone(opts)
		}
		updated = append(updated, agent.WithContinuationToken(""))
	}
	return updated
}

// prepareOptionsForLastIteration prepares options for the last iteration by removing schema tools.
//
// On the last iteration, we won't be processing any function calls, so we should not
// include function declarations in the request. This prevents the inner client
// from returning tool call requests that won't be handled.
func prepareOptionsForLastIteration(opts []agent.Option) []agent.Option {
	if len(opts) == 0 {
		return opts
	}

	var nonSchemaTools []tool.Tool
	removedAnySchemaTool := false
	for tl := range agent.AllOptions(opts, agent.WithTool) {
		if _, ok := tl.(tool.SchemaTool); ok {
			removedAnySchemaTool = true
			continue
		}
		nonSchemaTools = append(nonSchemaTools, tl)
	}

	if !removedAnySchemaTool {
		return opts
	}

	updated := make([]agent.Option, 0, len(opts)+len(nonSchemaTools)+1)
	for _, opt := range opts {
		if _, ok := opt.MAFValue().(tool.Tool); ok {
			continue
		}
		updated = append(updated, opt)
	}

	for _, tl := range nonSchemaTools {
		updated = append(updated, agent.WithTool(tl))
	}

	if len(nonSchemaTools) == 0 {
		updated = slices.DeleteFunc(updated, isToolModeOption)
	}

	return updated
}

func isToolModeOption(opt agent.Option) bool {
	return reflect.TypeOf(opt) == reflect.TypeOf(agent.WithToolMode(tool.ToolModeAuto))
}

func (f *autocall) shouldTerminateLoopBasedOnHandleableFunctions(ctx context.Context, funcCalls []*message.FunctionCallContent, tools map[string]tool.SchemaTool) bool {
	if len(funcCalls) == 0 {
		// There are no functions to call, so there's no reason to keep going.
		return true
	}
	if len(tools) == 0 {
		// There are functions to call but we have no tools, so we can't handle them.
		// If we're configured to terminate on unknown call requests, do so now.
		// Otherwise, processFunctionCalls will handle it by creating a NotFound response message.
		if f.terminateOnUnknownCalls {
			for _, fc := range funcCalls {
				f.logger.Warn(ctx, "function not found", "funcName", fc.Name)
			}
		}
		return f.terminateOnUnknownCalls
	}
	// At this point, we have both function call requests and some tools.
	// Look up each function.
	for _, fc := range funcCalls {
		declaration, ok := tools[fc.Name]
		if !ok {
			// The tool couldn't be found. If we're configured to terminate on unknown call requests,
			// break out of the loop now. Otherwise, processFunctionCalls will handle it by
			// creating a NotFound response message.
			if f.terminateOnUnknownCalls {
				f.logger.Warn(ctx, "function not found", "funcName", fc.Name)
				return true
			}
			continue
		}
		if _, ok := declaration.(tool.FuncTool); !ok {
			// The schema tool was found but it's not invocable. Regardless of TerminateOnUnknownCallRequests,
			// we need to break out of the loop so that callers can handle all the call requests.
			f.logger.Debug(ctx, "function is not invocable; terminating loop", "funcName", fc.Name)
			return true
		}
	}
	return false
}

func (f *autocall) createToolsMap(tools iter.Seq[tool.Tool]) (mtools map[string]tool.SchemaTool, anyRequiredApproval bool) {
	fn := func(t tool.Tool) {
		if !anyRequiredApproval {
			if approval, ok := t.(tool.ApprovalRequiredTool); ok && approval.ApprovalRequired() {
				anyRequiredApproval = true
			}
		}
		declaration, ok := t.(tool.SchemaTool)
		if !ok {
			return
		}
		if mtools == nil {
			mtools = make(map[string]tool.SchemaTool)
		}
		if _, exists := mtools[declaration.Name()]; exists {
			return
		}
		mtools[declaration.Name()] = declaration
	}
	for t := range tools {
		fn(t)
	}
	for _, t := range f.additionalTools {
		fn(t)
	}
	return mtools, anyRequiredApproval
}

func markServerHandledFunctionCalls(updates []*agent.ResponseUpdate, functionCalls []*message.FunctionCallContent) []*message.FunctionCallContent {
	if len(functionCalls) == 0 {
		return functionCalls
	}

	var resultCallIDs map[string]struct{}
	for _, update := range updates {
		if update == nil {
			continue
		}
		for _, c := range update.Contents {
			if frc, ok := c.(*message.FunctionResultContent); ok {
				if resultCallIDs == nil {
					resultCallIDs = make(map[string]struct{})
				}
				resultCallIDs[frc.CallID] = struct{}{}
			}
		}
	}

	if len(resultCallIDs) == 0 {
		return functionCalls
	}

	filtered := functionCalls[:0]
	for _, fcc := range functionCalls {
		if fcc == nil {
			continue
		}
		if _, ok := resultCallIDs[fcc.CallID]; ok {
			fcc.InformationalOnly = true
			continue
		}
		filtered = append(filtered, fcc)
	}
	return filtered
}

func hasAnyApprovalContent(msgs []*message.Message) bool {
	approvalResponseNeedsProcessing := approvalResponseNeedsProcessingByRequestID(msgs)
	return slices.ContainsFunc(msgs, func(m *message.Message) bool {
		if m == nil || m.Contents == nil {
			return false
		}
		return slices.ContainsFunc(m.Contents, func(c message.Content) bool {
			switch c := c.(type) {
			case *message.ToolApprovalRequestContent:
				if c == nil {
					return false
				}
				if responseNeedsProcessing, hasResponse := approvalResponseNeedsProcessing[c.RequestID]; hasResponse {
					return responseNeedsProcessing
				}
				return approvalToolCallNeedsProcessing(c.ToolCall)
			case *message.ToolApprovalResponseContent:
				if c == nil {
					return false
				}
				return approvalToolCallNeedsProcessing(c.ToolCall)
			default:
				return false
			}
		})
	})
}

func approvalToolCallNeedsProcessing(toolCall message.ToolCallContent) bool {
	fcc, ok := approvalToolCallAsFunctionCall(toolCall)
	return ok && !fcc.InformationalOnly
}

// approvalResponseNeedsProcessingByRequestID records whether paired approval
// responses are actionable. Any actionable duplicate keeps the pair actionable.
func approvalResponseNeedsProcessingByRequestID(msgs []*message.Message) map[string]bool {
	var result map[string]bool
	for _, msg := range msgs {
		if msg == nil {
			continue
		}
		for _, content := range msg.Contents {
			response, ok := content.(*message.ToolApprovalResponseContent)
			if !ok || response == nil {
				continue
			}
			fcc, ok := approvalToolCallAsFunctionCall(response.ToolCall)
			if !ok {
				continue
			}
			if result == nil {
				result = make(map[string]bool)
			}
			result[response.RequestID] = result[response.RequestID] || !fcc.InformationalOnly
		}
	}
	return result
}

type pendingApprovalRequestsState struct {
	Requests []*message.ToolApprovalRequestContent `json:"requests,omitempty"`
}

type pendingAutoApprovedCallsState struct {
	Calls []*message.FunctionCallContent `json:"calls,omitempty"`
}

func bindApprovalResponses(session *agent.Session, messages []*message.Message) ([]*message.Message, bool, error) {
	if session == nil {
		return messages, false, nil
	}

	known := make(map[string]*message.ToolApprovalRequestContent)
	var state pendingApprovalRequestsState
	if ok, err := session.Get(pendingApprovalRequestsStateKey, &state); err != nil {
		return nil, false, err
	} else if ok {
		for _, request := range state.Requests {
			if request != nil {
				known[request.RequestID] = request
			}
		}
	}

	out := make([]*message.Message, 0, len(messages))
	changed := false
	for _, msg := range messages {
		if msg == nil {
			out = append(out, nil)
			continue
		}
		contents := make(message.Contents, 0, len(msg.Contents))
		messageChanged := false
		for _, content := range msg.Contents {
			response, ok := content.(*message.ToolApprovalResponseContent)
			if !ok || response == nil {
				contents = append(contents, content)
				continue
			}

			request, found := known[response.RequestID]
			if !found {
				messageChanged = true
				continue
			}
			delete(known, response.RequestID)
			rebound := *response
			rebound.ToolCall = request.ToolCall
			contents = append(contents, &rebound)
			messageChanged = true
		}

		if !messageChanged {
			out = append(out, msg)
			continue
		}
		changed = true
		if len(contents) > 0 {
			clone := msg.Clone()
			clone.Contents = contents
			out = append(out, clone)
		}
	}
	if len(known) == 0 {
		session.Delete(pendingApprovalRequestsStateKey)
	} else {
		state.Requests = state.Requests[:0]
		for _, requestID := range slices.Sorted(maps.Keys(known)) {
			state.Requests = append(state.Requests, known[requestID])
		}
		session.Set(pendingApprovalRequestsStateKey, state)
	}
	if !changed {
		return messages, false, nil
	}
	return out, true, nil
}

func recordPendingApprovalRequests(session *agent.Session, contents message.Contents) error {
	if session == nil {
		return nil
	}
	var additions []*message.ToolApprovalRequestContent
	for _, content := range contents {
		if request, ok := content.(*message.ToolApprovalRequestContent); ok && request != nil {
			additions = append(additions, snapshotApprovalRequest(request))
		}
	}
	if len(additions) == 0 {
		return nil
	}

	var state pendingApprovalRequestsState
	if _, err := session.Get(pendingApprovalRequestsStateKey, &state); err != nil {
		return err
	}
	byID := make(map[string]int, len(state.Requests)+len(additions))
	for i, request := range state.Requests {
		if request != nil {
			byID[request.RequestID] = i
		}
	}
	for _, request := range additions {
		if _, ok := byID[request.RequestID]; ok {
			continue
		}
		byID[request.RequestID] = len(state.Requests)
		state.Requests = append(state.Requests, request)
	}
	session.Set(pendingApprovalRequestsStateKey, state)
	return nil
}

func injectPendingAutoApprovedCalls(session *agent.Session, messages []*message.Message) ([]*message.Message, bool, error) {
	if session == nil {
		return messages, false, nil
	}
	var state pendingAutoApprovedCallsState
	ok, err := session.Get(pendingAutoApprovedCallsStateKey, &state)
	if err != nil {
		return nil, false, err
	}
	if !ok || len(state.Calls) == 0 {
		return messages, false, nil
	}
	session.Delete(pendingAutoApprovedCallsStateKey)

	requests := make(message.Contents, 0, len(state.Calls))
	responses := make(message.Contents, 0, len(state.Calls))
	for _, call := range state.Calls {
		if call == nil {
			continue
		}
		request := &message.ToolApprovalRequestContent{
			RequestID: composeApprovalRequestID(call.CallID),
			ToolCall:  call,
		}
		requests = append(requests, request)
		responses = append(responses, request.CreateResponse(true, ""))
	}
	if len(requests) == 0 {
		return messages, false, nil
	}

	out := make([]*message.Message, 0, len(messages)+2)
	out = append(out, messages...)
	out = append(out,
		&message.Message{Role: message.RoleAssistant, Contents: requests},
		message.New(responses...),
	)
	return out, true, nil
}

func recordPendingAutoApprovedCalls(session *agent.Session, calls []*message.FunctionCallContent) error {
	if session == nil || len(calls) == 0 {
		return nil
	}
	var state pendingAutoApprovedCallsState
	if _, err := session.Get(pendingAutoApprovedCallsStateKey, &state); err != nil {
		return err
	}
	byID := make(map[string]int, len(state.Calls)+len(calls))
	for i, call := range state.Calls {
		if call != nil {
			byID[call.CallID] = i
		}
	}
	for _, call := range calls {
		if call == nil {
			continue
		}
		if i, ok := byID[call.CallID]; ok {
			state.Calls[i] = snapshotFunctionCall(call)
			continue
		}
		byID[call.CallID] = len(state.Calls)
		state.Calls = append(state.Calls, snapshotFunctionCall(call))
	}
	session.Set(pendingAutoApprovedCallsStateKey, state)
	return nil
}

func snapshotApprovalRequest(request *message.ToolApprovalRequestContent) *message.ToolApprovalRequestContent {
	if request == nil {
		return nil
	}
	clone := &message.ToolApprovalRequestContent{RequestID: request.RequestID}
	switch call := request.ToolCall.(type) {
	case *message.FunctionCallContent:
		if call != nil {
			callClone := *call
			callClone.AdditionalProperties = maps.Clone(call.AdditionalProperties)
			callClone.Annotations = slices.Clone(call.Annotations)
			clone.ToolCall = &callClone
		}
	case *message.MCPServerToolCallContent:
		if call != nil {
			callClone := *call
			callClone.AdditionalProperties = maps.Clone(call.AdditionalProperties)
			callClone.Annotations = slices.Clone(call.Annotations)
			clone.ToolCall = &callClone
		}
	default:
		clone.ToolCall = request.ToolCall
	}
	return clone
}

func snapshotFunctionCall(call *message.FunctionCallContent) *message.FunctionCallContent {
	if call == nil {
		return nil
	}
	clone := *call
	clone.AdditionalProperties = maps.Clone(call.AdditionalProperties)
	clone.Annotations = slices.Clone(call.Annotations)
	return &clone
}

func (f *autocall) invokeApprovedToolApprovalResponses(ctx context.Context, approvals []toolApprovalResultWithRequestMessage, tools map[string]tool.SchemaTool, errCount int) (*message.Message, int, error) {
	// Check if there are any function calls to do for any approved tool requests and execute them.
	if len(approvals) == 0 {
		return nil, errCount, nil
	}
	funcCalls := make([]*message.FunctionCallContent, 0, len(approvals))
	for _, approval := range approvals {
		if approval.Response != nil {
			if fcc, ok := approvalToolCallAsFunctionCall(approval.Response.ToolCall); ok {
				funcCalls = append(funcCalls, fcc)
			}
		}
	}
	if len(funcCalls) == 0 {
		return nil, errCount, nil
	}
	newMsg, errCount, err := f.processFunctionCalls(ctx, tools, funcCalls, errCount)
	if err != nil {
		return nil, errCount, err
	}

	return newMsg, errCount, nil
}

type functionInvocationStatus int

const (
	functionInvocationStatusRanToCompletion functionInvocationStatus = iota
	functionInvocationStatusNotFound
	functionInvocationStatusException
)

type functionInvocationResult struct {
	status functionInvocationStatus
	call   *message.FunctionCallContent
	result any
	err    error
}

func (f *autocall) processFunctionCalls(ctx context.Context, tools map[string]tool.SchemaTool, funcCalls []*message.FunctionCallContent, errCount int) (*message.Message, int, error) {
	// We must add a response for every tool call, regardless of whether we successfully executed it or not.
	// If we successfully execute it, we'll add the result. If we don't, we'll add an error.
	if len(funcCalls) == 0 { // invariant
		panic("function calls expected")
	}
	captureCurrentIterationErrors := errCount < f.maximumConsecutiveErrorsPerRequest
	// Process all functions. If there's more than one and concurrent invocation is enabled, do so in parallel.
	results := make([]functionInvocationResult, 0, len(funcCalls))
	if len(funcCalls) > 1 && f.allowConcurrentInvocations {
		// Rather than awaiting each function before invoking the next, invoke all of them
		// and then await all of them. We avoid forcibly introducing parallelism via Task.Run,
		// but if a function invocation completes asynchronously, its processing can overlap
		// with the processing of other the other invocation invocations.
		parallelResults := make([]functionInvocationResult, len(funcCalls))
		var wg sync.WaitGroup
		wg.Add(len(funcCalls))
		for i, fc := range funcCalls {
			go func() {
				defer wg.Done()
				parallelResults[i] = f.processFunctionCall(ctx, tools, fc)
			}()
		}
		wg.Wait()
		results = parallelResults
	} else {
		// Invoke each function serially.
		for _, fc := range funcCalls {
			result := f.processFunctionCall(ctx, tools, fc)
			if !captureCurrentIterationErrors && result.status == functionInvocationStatusException {
				return nil, errCount, result.err
			}
			results = append(results, result)
		}
	}
	for _, result := range results {
		result.call.InformationalOnly = true
	}

	newMsg := f.createResponseMessage(results)
	var err error
	errCount, err = f.updateConsecutiveErrorCountOrThrow(ctx, newMsg, errCount)
	if err != nil {
		return nil, errCount, err
	}

	return newMsg, errCount, nil
}

func (f *autocall) updateConsecutiveErrorCountOrThrow(ctx context.Context, added *message.Message, errCount int) (int, error) {
	var errs []error
	if added != nil {
		for _, content := range added.Contents {
			if frc, ok := content.(*message.FunctionResultContent); ok && frc.Error != nil {
				errs = append(errs, frc.Error)
			}
		}
	}

	// Update consecutive error count
	if len(errs) > 0 {
		errCount++
		if errCount > f.maximumConsecutiveErrorsPerRequest {
			f.logger.Warn(ctx, "maximum consecutive errors exceeded; throwing aggregated errors", "maximumConsecutiveErrorsPerRequest", f.maximumConsecutiveErrorsPerRequest)
			if len(errs) == 1 {
				return errCount, errs[0]
			}
			return errCount, errors.Join(errs...)
		}
	} else {
		errCount = 0
	}

	return errCount, nil
}

func (f *autocall) processFunctionCall(ctx context.Context, tools map[string]tool.SchemaTool, funcCall *message.FunctionCallContent) functionInvocationResult {
	declaration, ok := tools[funcCall.Name]
	if !ok {
		f.logger.Warn(ctx, "function not found", "funcName", funcCall.Name)
		return functionInvocationResult{status: functionInvocationStatusNotFound, call: funcCall}
	}
	tl, ok := declaration.(tool.FuncTool)
	if !ok {
		f.logger.Debug(ctx, "function is not invocable; returning not found result", "funcName", funcCall.Name)
		return functionInvocationResult{status: functionInvocationStatusNotFound, call: funcCall}
	}
	f.logger.Debug(ctx, "calling function", "funcName", funcCall.Name, slogx.SensitiveData("arguments", funcCall.Arguments))
	start := time.Now()
	ctx, span := startToolSpan(ctx, funcCall, declaration)
	if span != nil {
		defer span.End()
	}
	var result any
	var err error
	func() {
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(error); ok {
					err = fmt.Errorf("tool execution panic: %w", e)
				} else {
					err = fmt.Errorf("tool execution panic: %v", r)
				}
			}
		}()
		result, err = tl.Call(ctx, funcCall.Arguments)
	}()
	if err != nil {
		if span != nil {
			span.SetAttributes(attribute.String(attrKeyErrorType, otelx.ErrorTypeName(err)))
			span.RecordError(err, trace.WithTimestamp(time.Now()))
			span.SetStatus(codes.Error, err.Error())
		}
		if errors.Is(err, context.Canceled) {
			f.logger.Debug(ctx, "call canceled", "funcName", funcCall.Name)
		} else {
			f.logger.Error(ctx, "call failed", "funcName", funcCall.Name, "error", err)
		}
	}
	f.logger.Debug(ctx, "function call completed", "funcName", funcCall.Name, "duration", time.Since(start), slogx.SensitiveData("result", result))

	if err != nil {
		return functionInvocationResult{status: functionInvocationStatusException, call: funcCall, err: err}
	}

	return functionInvocationResult{status: functionInvocationStatusRanToCompletion, call: funcCall, result: result}
}

func startToolSpan(ctx context.Context, funcCall *message.FunctionCallContent, tl tool.Tool) (context.Context, trace.Span) {
	tracer, ok := otelx.TracerFromContext(ctx)
	if !ok || funcCall == nil {
		return ctx, nil
	}
	name := opExecuteTool
	if funcCall.Name != "" {
		name += " " + funcCall.Name
	}
	attrs := []attribute.KeyValue{
		attribute.String(attrKeyOperationName, opExecuteTool),
		attribute.String(attrKeyToolName, funcCall.Name),
		attribute.String(attrKeyToolCallID, cmp.Or(funcCall.CallID, "unknown")),
		attribute.String(attrKeyToolType, "function"),
	}
	if tl != nil {
		if desc := tl.Description(); desc != "" {
			attrs = append(attrs, attribute.String(attrKeyToolDesc, desc))
		}
	}
	// TODO: add gen_ai.tool.call.arguments and gen_ai.tool.call.result when an
	// opt-in EnableSensitiveData flag is available on Config (parity with Python's
	// get_function_span_attributes and .NET's OpenTelemetryAgent.EnableSensitiveData).
	return tracer.Start(ctx, name, trace.WithAttributes(attrs...))
}

func (f *autocall) createResponseMessage(results []functionInvocationResult) *message.Message {
	contents := make([]message.Content, len(results))
	for i, result := range results {
		contents[i] = f.createFunctionResultContent(result)
	}
	return &message.Message{Role: message.RoleTool, Contents: contents}
}

func (f *autocall) createFunctionResultContent(result functionInvocationResult) *message.FunctionResultContent {
	if result.status == functionInvocationStatusRanToCompletion {
		if frc, ok := result.result.(*message.FunctionResultContent); ok && frc.CallID == result.call.CallID {
			return frc
		}

		functionResult := result.result
		if functionResult == nil {
			functionResult = "Success: Function completed."
		}

		return &message.FunctionResultContent{
			CallID: result.call.CallID,
			Result: functionResult,
		}
	}

	functionResult := "Error: Unknown error."
	switch result.status {
	case functionInvocationStatusNotFound:
		functionResult = fmt.Sprintf("Error: Requested function %q not found.", result.call.Name)
	case functionInvocationStatusException:
		functionResult = "Error: Function failed."
		if f.includeDetailedErrors && result.err != nil {
			functionResult = fmt.Sprintf("%s Exception: %v", functionResult, result.err)
		}
	}

	return &message.FunctionResultContent{
		CallID: result.call.CallID,
		Error:  result.err,
		Result: functionResult,
	}
}

// processToolApprovalResponses do the following:
//   - Removes all ToolApprovalRequestContent and ToolApprovalResponseContent from msgs.
//   - Recreates tool call content for any ToolApprovalResponseContent that hasn't been handled yet.
//   - Generates failed FunctionResultContent for any rejected function tool call.
//   - Adds all the new content items to originalMessages and returns them as the pre-invocation history.
func (f *autocall) processToolApprovalResponses(ctx context.Context, msgs []*message.Message, toolMsgID, fallbackMsgID string) ([]*message.Message, []*message.Message, []toolApprovalResultWithRequestMessage, error) {
	// Extract any approval responses where we need to execute or reject the function calls.
	// The original messages are also modified to remove all approval requests and responses.
	msgs, approvals, rejections, err := f.extractAndRemoveToolApprovalRequestsAndResponses(ctx, msgs)
	if err != nil {
		return nil, nil, nil, err
	}
	// Wrap the tool call content in message(s).
	preDownstreamCallHistory := convertToToolCallContentMessages(append(rejections, approvals...), fallbackMsgID)
	// Generate failed function result contents for any rejected requests and wrap it in a message.
	rejectedFunctionContent := f.generateRejectedFunctionResults(ctx, rejections)
	var rejectedPreDownstreamCallResultsMsgs *message.Message
	if len(rejectedFunctionContent) > 0 {
		rejectedPreDownstreamCallResultsMsgs = &message.Message{
			Role:     message.RoleTool,
			ID:       toolMsgID,
			Contents: rejectedFunctionContent,
		}
	}
	// Add generated tool call and function result content to the pre-downstream-call history so they can be returned to the caller as part of the next response.
	// Also, add them into the original messages list so that they are passed to the inner client and can be used to generate a result.
	if rejectedPreDownstreamCallResultsMsgs != nil {
		preDownstreamCallHistory = append(preDownstreamCallHistory, rejectedPreDownstreamCallResultsMsgs)
		msgs = append(msgs, rejectedPreDownstreamCallResultsMsgs)
	}
	return msgs, preDownstreamCallHistory, approvals, nil
}

type toolApprovalResultWithRequestMessage struct {
	Response       *message.ToolApprovalResponseContent
	RequestMessage *message.Message
}

func approvalToolCallAsFunctionCall(toolCall message.ToolCallContent) (*message.FunctionCallContent, bool) {
	fcc, ok := toolCall.(*message.FunctionCallContent)
	return fcc, ok && fcc != nil
}

func cloneToolApprovalResponseContent(content *message.ToolApprovalResponseContent) *message.ToolApprovalResponseContent {
	if content == nil {
		return nil
	}
	cloned := *content
	if functionCall, ok := approvalToolCallAsFunctionCall(content.ToolCall); ok {
		clonedFunctionCall := *functionCall
		cloned.ToolCall = &clonedFunctionCall
	}
	return &cloned
}

func (f *autocall) extractAndRemoveToolApprovalRequestsAndResponses(ctx context.Context, msgs []*message.Message) (out []*message.Message, approvals, rejections []toolApprovalResultWithRequestMessage, err error) {
	var (
		allApprovalRequestsMessages map[string]toolApprovalResultWithRequestMessage
		approvalRequestCallIDs      map[string]struct{}
		functionResultCallIds       map[string]struct{}
		allApprovalResponses        []*message.ToolApprovalResponseContent
	)
	// A paired response is authoritative because request and response tool calls may
	// be separate instances after serialization.
	approvalResponseNeedsProcessing := approvalResponseNeedsProcessingByRequestID(msgs)
	// 1st iteration, over all messages and content:
	// - Build a list of all function call ids that are already executed.
	// - Build a list of all function approval requests and responses.
	// - Build a list of the content we want to keep (everything except approval requests and responses) and create a new list of messages for those.
	// - Validate that we have an approval response for each approval request.
	var anyRemoved bool
	for i, msg := range msgs {
		if msg == nil {
			continue
		}
		var keptContents []message.Content
		for _, c := range msg.Contents {
			switch c := c.(type) {
			case *message.ToolApprovalRequestContent:
				fcc, ok := approvalToolCallAsFunctionCall(c.ToolCall)
				responseNeedsProcessing, hasResponse := approvalResponseNeedsProcessing[c.RequestID]
				requestNeedsProcessing := ok && ((hasResponse && responseNeedsProcessing) || (!hasResponse && !fcc.InformationalOnly))
				if !requestNeedsProcessing {
					keptContents = append(keptContents, c)
					continue
				}
				// Validation: Capture each call id for each approval request to ensure later we have a matching response.
				if approvalRequestCallIDs == nil {
					approvalRequestCallIDs = make(map[string]struct{})
				}
				approvalRequestCallIDs[fcc.CallID] = struct{}{}
				if allApprovalRequestsMessages == nil {
					allApprovalRequestsMessages = make(map[string]toolApprovalResultWithRequestMessage)
				}
				allApprovalRequestsMessages[c.RequestID] = toolApprovalResultWithRequestMessage{RequestMessage: msg}
			case *message.ToolApprovalResponseContent:
				fcc, ok := approvalToolCallAsFunctionCall(c.ToolCall)
				if !ok {
					keptContents = append(keptContents, c)
					continue
				}
				if fcc.InformationalOnly {
					keptContents = append(keptContents, c)
					continue
				}
				// Validation: Remove the call id for each approval response, to check it off the list of requests we need responses for.
				delete(approvalRequestCallIDs, fcc.CallID)
				allApprovalResponses = append(allApprovalResponses, c)
			case *message.FunctionResultContent:
				// Maintain a list of function calls that have already been invoked to avoid invoking them twice.
				if functionResultCallIds == nil {
					functionResultCallIds = make(map[string]struct{})
				}
				functionResultCallIds[c.CallID] = struct{}{}
				keptContents = append(keptContents, c)
			default:
				keptContents = append(keptContents, c)
			}
		}
		if len(keptContents) != len(msg.Contents) {
			// If any contents were filtered out, we need to either remove the message entirely
			// (if no contents remain) or create a new message with the filtered contents.
			if len(keptContents) > 0 {
				// Create a new replacement message to store the filtered contents.
				newMsg := msg.Clone()
				newMsg.Contents = keptContents
				msgs[i] = newMsg
			} else {
				// Remove the message entirely since it has no contents left. Rather than doing an O(N) removal, which could possibly
				// result in an O(N^2) overall operation, we mark the message as nil and then do a single pass removal of all nils after the loop.
				anyRemoved = true
				msgs[i] = nil
			}
		}
	}
	if anyRemoved {
		// Clean up any messages that were marked for removal during the iteration.
		msgs = slices.DeleteFunc(msgs, func(m *message.Message) bool {
			return m == nil
		})
	}
	if len(approvalRequestCallIDs) > 0 {
		// Validation: If we got an approval for each request, we should have no call ids left.
		// Collect call IDs for error message
		callIDs := slices.Collect(maps.Keys(approvalRequestCallIDs))
		slices.Sort(callIDs) // Sort for consistent error messages
		return nil, nil, nil, fmt.Errorf("ToolApprovalRequestContent found with ToolCall.CallID(s) '%s' that have no matching ToolApprovalResponseContent", strings.Join(callIDs, "', '"))
	}

	// 2nd iteration, over all approval responses:
	// - Filter out any approval responses that already have a matching function result (i.e. already executed).
	// - Find the matching function approval request for any response (where available).
	// - Split the approval responses into two lists: approved and rejected, with their request messages (where available).
	for _, approvalResponse := range allApprovalResponses {
		if approvalResponse.ToolCall != nil {
			if _, found := functionResultCallIds[approvalResponse.ToolCall.GetCallID()]; found {
				// Skip any approval responses that have already been processed.
				continue
			}
		}
		if fcc, ok := approvalToolCallAsFunctionCall(approvalResponse.ToolCall); ok {
			f.logger.Debug(ctx, "processing approval response", "funcName", fcc.Name, "approved", approvalResponse.Approved)
		}
		request := allApprovalRequestsMessages[approvalResponse.RequestID]
		// Split the responses into approved and rejected.
		newMsg := toolApprovalResultWithRequestMessage{
			Response:       cloneToolApprovalResponseContent(approvalResponse),
			RequestMessage: request.RequestMessage,
		}
		if approvalResponse.Approved {
			approvals = append(approvals, newMsg)
		} else {
			rejections = append(rejections, newMsg)
		}

	}
	return msgs, approvals, rejections, nil
}

func convertToToolCallContentMessages(messages []toolApprovalResultWithRequestMessage, fallbackMessageID string) []*message.Message {
	var currentMsg *message.Message
	var messagesByID map[string]*message.Message
	var messageOrder []*message.Message // Track insertion order since Go maps don't preserve order
	for _, msg := range messages {
		if msg.Response == nil || msg.Response.ToolCall == nil {
			continue
		}

		// Don't need to create a dictionary if we already have one or if it's the first iteration.
		shouldCreateMap := messagesByID == nil && currentMsg != nil &&
			// Everywhere we have no RequestMessage we use the fallbackMessageID, so in this case there is only one message.
			(msg.RequestMessage != nil || currentMsg.ID != fallbackMessageID) &&
			// Where we do have a RequestMessage, we can check if its message id differs from the current one.
			(msg.RequestMessage != nil && currentMsg.ID != msg.RequestMessage.ID)
		if shouldCreateMap {
			// The majority of the time, all FCC would be part of a single message, so no need to create a dictionary for this case.
			// If we are dealing with multiple messages though, we need to keep track of them by their message ID.
			messagesByID = make(map[string]*message.Message)
			previousMsgID := currentMsg.ID
			if previousMsgID == fallbackMessageID {
				previousMsgID = ""
			}
			messagesByID[previousMsgID] = currentMsg
			messageOrder = append(messageOrder, currentMsg)
		}

		// When RequestMessage is nil, use empty string as lookup key (not fallbackMessageID)
		// This matches .NET behavior which uses string.Empty for null RequestMessage
		msgID := ""
		if msg.RequestMessage != nil {
			msgID = msg.RequestMessage.ID
		}

		// Check if we already have a message with this ID.
		var foundMsg *message.Message
		if messagesByID != nil {
			foundMsg = messagesByID[msgID]
		} else if currentMsg != nil {
			// If we don't have a map yet, check if currentMsg matches.
			// For nil RequestMessage (msgID=""), we need to check if currentMsg also came from nil RequestMessage.
			// We can detect this by checking if currentMsg.ID is the fallbackMessageID.
			if msgID == "" && currentMsg.ID == fallbackMessageID {
				foundMsg = currentMsg
			} else if msgID != "" && currentMsg.ID == msgID {
				foundMsg = currentMsg
			}
		}

		if foundMsg == nil {
			currentMsg = convertToToolCallContentMessage(msg, fallbackMessageID)
			if messagesByID != nil {
				messageOrder = append(messageOrder, currentMsg)
			}
		} else {
			foundMsg.Contents = append(foundMsg.Contents, msg.Response.ToolCall)
			currentMsg = foundMsg
		}
		if messagesByID != nil {
			// Store with msgID as key, not currentMsg.ID, because we look up by msgID.
			messagesByID[msgID] = currentMsg
		}
	}
	if messagesByID != nil {
		return messageOrder
	}
	if currentMsg != nil {
		return []*message.Message{currentMsg}
	}
	return nil
}

func convertToToolCallContentMessage(msg toolApprovalResultWithRequestMessage, fallbackMessageID string) *message.Message {
	var newMsg *message.Message
	if msg.RequestMessage != nil {
		newMsg = msg.RequestMessage.Clone()
	} else {
		newMsg = &message.Message{
			Role: message.RoleAssistant,
		}
	}
	newMsg.Contents = []message.Content{msg.Response.ToolCall}
	newMsg.ID = cmp.Or(newMsg.ID, fallbackMessageID)
	return newMsg
}

func (f *autocall) generateRejectedFunctionResults(ctx context.Context, rejections []toolApprovalResultWithRequestMessage) []message.Content {
	if len(rejections) == 0 {
		return nil
	}
	contents := make([]message.Content, 0, len(rejections))
	for _, rej := range rejections {
		fcc, ok := approvalToolCallAsFunctionCall(rej.Response.ToolCall)
		if !ok {
			continue
		}
		f.logger.Debug(ctx, "function was rejected", "funcName", fcc.Name, "reason", rej.Response.Reason)
		fcc.InformationalOnly = true
		result := "Tool call invocation rejected."
		if strings.TrimSpace(rej.Response.Reason) != "" {
			result += " " + rej.Response.Reason
		}
		resultContent := &message.FunctionResultContent{
			CallID: fcc.CallID,
			Result: result,
		}
		contents = append(contents, resultContent)
	}
	return contents
}
