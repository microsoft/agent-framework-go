// Copyright (c) Microsoft. All rights reserved.

package autocall

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/internal/slogx"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
)

type Config struct {
	Logger                             *slog.Logger
	LogSensitiveData                   bool
	AdditionalTools                    []tool.Tool
	IncludeDetailedErrors              bool
	TerminateOnUnknownCalls            bool
	AllowConcurrentInvocations         bool
	MaximumConsecutiveErrorsPerRequest int
	MaximumIterationsPerRequest        int // Default: 40
	NewID                              func() string
}

type autocall struct {
	logger                             slogx.Logger
	additionalTools                    []tool.Tool
	includeDetailedErrors              bool
	terminateOnUnknownCalls            bool
	allowConcurrentInvocations         bool
	maximumConsecutiveErrorsPerRequest int
	maximumIterationsPerRequest        int
	newID                              func() string
}

// New creates a new function-invoking chat client that wraps the provided client.
func New(cfg Config) middleware.Middleware {
	if cfg.NewID == nil {
		cfg.NewID = uuid.NewString
	}
	ac := &autocall{
		logger: slogx.Logger{
			Logger:        cfg.Logger,
			SensitiveData: cfg.LogSensitiveData,
			Type:          slogx.TypeMiddleware,
			Name:          "autocall",
		},
		additionalTools:                    cfg.AdditionalTools,
		includeDetailedErrors:              cfg.IncludeDetailedErrors,
		terminateOnUnknownCalls:            cfg.TerminateOnUnknownCalls,
		allowConcurrentInvocations:         cfg.AllowConcurrentInvocations,
		maximumConsecutiveErrorsPerRequest: cfg.MaximumConsecutiveErrorsPerRequest,
		maximumIterationsPerRequest:        cmp.Or(cfg.MaximumIterationsPerRequest, 40),
		newID:                              cfg.NewID,
	}
	return ac
}

func (f *autocall) Run(ctx context.Context, next middleware.RunFunc, messages []*message.Message, opts ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		tools, requiresApproval := f.createToolsMap(agentopt.All(opts, agentopt.Tool))

		// This is a synthetic ID since we're generating the tool messages instead of getting them from
		// the underlying provider. When emitting the streamed chunks, it's perfectly valid for us to
		// use the same message ID for all of them within a given iteration, as this is a single logical
		// message with multiple content items. We could also use different message IDs per tool content,
		// but there's no benefit to doing so.
		toolMsgID := f.newID()
		var errCount int
		if hasAnyApprovalContent(messages) {
			messages = slices.Clone(messages)
			// We also need a synthetic ID for the function call content for approved function calls
			// where we don't know what the original message id of the function call was.
			funcCallFallbackMsgID := f.newID()

			// A previous turn may have translated FunctionCallContents from the inner client into approval requests sent back to the caller,
			// for any functions that were actually ApprovalRequiredFunctions. If the incoming chat messages include responses to those
			// approval requests, we need to process them now. This entails removing these manufactured approval requests from the chat message
			// list and replacing them with the appropriate FunctionCallContents and FunctionResultContents that would have been generated if
			// the inner client had returned them directly.
			var notInvokedMsgs []approvalResultWithRequestMessage
			var preDownstreamCallHistory []*message.Message
			var err error
			messages, preDownstreamCallHistory, notInvokedMsgs, err = processFunctionApprovalResponses(messages, toolMsgID, funcCallFallbackMsgID)
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
			newMsg, errCount, err = f.invokeApprovedFunctionApprovalResponses(ctx, notInvokedMsgs, tools, 0)
			if err != nil {
				yield(nil, err)
				return
			}
			if newMsg != nil {
				messages = append(messages, newMsg)
				newMsg.ID = toolMsgID
				if !yield(convertToolResultMsgToUpdate(newMsg, toolMsgID), nil) {
					return
				}
			}
		}
		// At this point, we've fully handled all approval responses that were part of the original messages,
		// and we can now enter the main function calling loop.
		var updates []*message.ResponseUpdate
		var functionCallContents []*message.FunctionCallContent
		var approvalRequiredFunctions []tool.Tool
		for i := 0; ; i++ {
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
					if fcc, ok := c.(*message.FunctionCallContent); ok {
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
				if requiresApproval && approvalRequiredFunctions == nil && len(functionCallContents) > 0 {
					for tl := range agentopt.All(opts, agentopt.Tool) {
						if tl, ok := tl.(tool.ApprovalRequiredTool); ok {
							approvalRequiredFunctions = append(approvalRequiredFunctions, tl)
						}
					}
					for _, tl := range f.additionalTools {
						if tl, ok := tl.(tool.ApprovalRequiredTool); ok {
							approvalRequiredFunctions = append(approvalRequiredFunctions, tl)
						}
					}
				}
				if len(approvalRequiredFunctions) == 0 || len(functionCallContents) == 0 {
					// If there are no function calls to make yet, or if none of the functions require approval at all,
					// we can yield the update as-is.
					lastYieldedUpdateIdx++
					if !yield(update, nil) {
						return
					}
					continue
				}
				hasApprovalRequiringFcc, lastApprovalCheckedFCCIdx = checkForApprovalRequiringFCC(functionCallContents, approvalRequiredFunctions, hasApprovalRequiringFcc, lastApprovalCheckedFCCIdx)
				if hasApprovalRequiringFcc {
					// If we've encountered a function call content that requires approval,
					// we need to ask for approval for all functions, since we cannot mix and match.
					// Convert all function call contents into approval requests from the last yielded update index
					// and yield all those updates.
					for ; lastYieldedUpdateIdx < len(updates); lastYieldedUpdateIdx++ {
						updateToYield := updates[lastYieldedUpdateIdx]
						updatedContent := tryReplaceFunctionCallsWithApprovalRequests(updateToYield.Contents)
						if updatedContent != nil {
							updateToYield.Contents = updatedContent
						}
						if !yield(updateToYield, nil) {
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
			// We need to yield any remaining updates that were not yielded while looping through the streamed updates.
			for ; lastYieldedUpdateIdx < len(updates); lastYieldedUpdateIdx++ {
				if !yield(updates[lastYieldedUpdateIdx], nil) {
					return
				}
			}
			// If there's nothing more to do, break out of the loop and allow the handling at the
			// end to configure the response with aggregated data from previous requests.
			if i >= f.maximumIterationsPerRequest || hasApprovalRequiringFcc || f.shouldTerminateLoopBasedOnHandleableFunctions(functionCallContents, tools) {
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

			// Use the augmented history as the new set of messages to send.
			opts = updateOptionsForNextIteration(opts)
			messages = []*message.Message{newMsg}
		}
	}
}

func tryReplaceFunctionCallsWithApprovalRequests(contents []message.Content) []message.Content {
	var updated []message.Content
	for i, c := range contents {
		if fcc, ok := c.(*message.FunctionCallContent); ok {
			if updated == nil {
				updated = slices.Clone(contents)
			}
			updated[i] = &message.FunctionApprovalRequestContent{
				FunctionCall: fcc,
				ID:           fcc.CallID,
			}
		}
	}
	return updated
}

//	checkForApprovalRequiringFCC checks if any of the provided functionCallContents require approval.
//
// Supports checking from a provided index up to the end of the list, to allow efficient incremental checking when streaming.
func checkForApprovalRequiringFCC(functionCalls []*message.FunctionCallContent, approvalRequiredFunctions []tool.Tool, hasApprovalRequiringFcc bool, lastApprovalCheckedFCCIdx int) (bool, int) {
	if hasApprovalRequiringFcc {
		// If we already found an approval requiring FCC, we can skip checking the rest.
		return true, lastApprovalCheckedFCCIdx
	}
	for ; lastApprovalCheckedFCCIdx < len(functionCalls); lastApprovalCheckedFCCIdx++ {
		fcc := functionCalls[lastApprovalCheckedFCCIdx]
		for _, t := range approvalRequiredFunctions {
			if t.Name() == fcc.Name {
				hasApprovalRequiringFcc = true
				break
			}
		}
	}
	return hasApprovalRequiringFcc, lastApprovalCheckedFCCIdx
}

func convertToolResultMsgToUpdate(msg *message.Message, msgID string) *message.ResponseUpdate {
	return &message.ResponseUpdate{
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

func updateOptionsForNextIteration(opts []agentopt.RunOption) []agentopt.RunOption {
	if v, ok := agentopt.Get(opts, agentopt.ToolMode); ok && v == tool.ToolModeRequired {
		// We have to reset the tool mode to be non-required after the first iteration,
		// as otherwise we'll be in an infinite loop.
		opts = append(opts, agentopt.ToolMode(tool.ToolModeAuto))
	}
	// Reset the continuation token of a background response operation
	// to signal the inner client to handle function call result rather
	// than getting the result of the operation.
	if _, ok := agentopt.Get(opts, agentopt.ContinuationToken); ok {
		opts = append(opts, agentopt.ContinuationToken(nil))
	}
	return opts
}

func (f *autocall) shouldTerminateLoopBasedOnHandleableFunctions(funcCalls []*message.FunctionCallContent, tools map[string]tool.Tool) bool {
	if len(funcCalls) == 0 {
		// There are no functions to call, so there's no reason to keep going.
		return true
	}
	if len(tools) == 0 {
		// There are functions to call but we have no tools, so we can't handle them.
		// If we're configured to terminate on unknown call requests, do so now.
		// Otherwise, processFunctionCalls will handle it by creating a NotFound response message.
		return f.terminateOnUnknownCalls
	}
	// At this point, we have both function call requests and some tools.
	// Look up each function.
	for _, fc := range funcCalls {
		tl, ok := tools[fc.Name]
		if !ok {
			// The tool couldn't be found. If we're configured to terminate on unknown call requests,
			// break out of the loop now. Otherwise, processFunctionCalls will handle it by
			// creating a NotFound response message.
			if f.terminateOnUnknownCalls {
				return true
			}
			continue
		}
		if _, ok := tl.(tool.FuncTool); !ok {
			// The tool was found but it's not invocable. Regardless of TerminateOnUnknownCallRequests,
			// we need to break out of the loop so that callers can handle all the call requests.
			return true
		}
	}
	return false
}

func (f *autocall) createToolsMap(tools iter.Seq[tool.Tool]) (mtools map[string]tool.Tool, anyRequiredApproval bool) {
	fn := func(t tool.Tool) {
		if mtools == nil {
			mtools = make(map[string]tool.Tool)
		}
		mtools[t.Name()] = t
		if !anyRequiredApproval {
			if _, ok := t.(tool.ApprovalRequiredTool); ok {
				anyRequiredApproval = true
			}
		}
	}
	for _, t := range f.additionalTools {
		fn(t)
	}
	for t := range tools {
		fn(t)
	}
	return mtools, anyRequiredApproval
}

func hasAnyApprovalContent(msgs []*message.Message) bool {
	return slices.ContainsFunc(msgs, func(m *message.Message) bool {
		if m == nil || m.Contents == nil {
			return false
		}
		return slices.ContainsFunc(m.Contents, func(c message.Content) bool {
			switch c.(type) {
			case *message.FunctionApprovalRequestContent, *message.FunctionApprovalResponseContent:
				return true
			default:
				return false
			}
		})
	})

}

func (f *autocall) invokeApprovedFunctionApprovalResponses(ctx context.Context, approvals []approvalResultWithRequestMessage, tools map[string]tool.Tool, errCount int) (*message.Message, int, error) {
	// Check if there are any function calls to do for any approved functions and execute them.
	if len(approvals) == 0 {
		return nil, errCount, nil
	}
	// Check if there are any function calls to do for any approved functions and execute them.
	funcCalls := make([]*message.FunctionCallContent, 0, len(approvals))
	for _, approval := range approvals {
		if approval.Response != nil {
			funcCalls = append(funcCalls, approval.Response.FunctionCall)
		}
	}
	return f.processFunctionCalls(ctx, tools, funcCalls, errCount)
}

func (f *autocall) processFunctionCalls(ctx context.Context, tools map[string]tool.Tool, funcCalls []*message.FunctionCallContent, errCount int) (*message.Message, int, error) {
	// We must add a response for every tool call, regardless of whether we successfully executed it or not.
	// If we successfully execute it, we'll add the result. If we don't, we'll add an error.
	if len(funcCalls) == 0 { // invariant
		panic("function calls expected")
	}
	// Process all functions. If there's more than one and concurrent invocation is enabled, do so in parallel.
	results := make([]message.Content, len(funcCalls))
	if len(funcCalls) > 1 && f.allowConcurrentInvocations {
		// Rather than awaiting each function before invoking the next, invoke all of them
		// and then await all of them. We avoid forcibly introducing parallelism via Task.Run,
		// but if a function invocation completes asynchronously, its processing can overlap
		// with the processing of other the other invocation invocations.
		var wg sync.WaitGroup
		wg.Add(len(funcCalls))
		for i, fc := range funcCalls {
			go func() {
				defer wg.Done()
				results[i] = f.processFunctionCall(ctx, tools, fc)
			}()
		}
		wg.Wait()
	} else {
		// Invoke each function serially.
		for i, fc := range funcCalls {
			results[i] = f.processFunctionCall(ctx, tools, fc)
		}
	}
	// Check if any function call in this iteration had an error
	var errs []error
	for _, resultContent := range results {
		if frc, ok := resultContent.(*message.FunctionResultContent); ok && frc.Error != nil {
			errs = append(errs, frc.Error)
		}
	}

	// Update consecutive error count
	if len(errs) > 0 {
		errCount++
		if errCount > f.maximumConsecutiveErrorsPerRequest {
			return nil, errCount, errors.Join(errs...)
		}
	} else {
		errCount = 0
	}

	return &message.Message{Role: message.RoleTool, Contents: results}, errCount, nil
}

func (f *autocall) processFunctionCall(ctx context.Context, tools map[string]tool.Tool, funcCall *message.FunctionCallContent) *message.FunctionResultContent {
	var tl tool.FuncTool
	if v, ok := tools[funcCall.Name]; ok {
		if ft, ok := v.(tool.FuncTool); ok {
			tl = ft
		}
	}
	if tl == nil {
		return f.createFunctionResult(
			funcCall.CallID,
			fmt.Sprintf("Error: Requested function %q not found.", funcCall.Name),
			nil,
		)
	}
	f.logger.Debug(ctx, "calling function", "funcName", funcCall.Name, slogx.SensitiveData("arguments", funcCall.Arguments))
	start := time.Now()
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
		if errors.Is(err, context.Canceled) {
			f.logger.Debug(ctx, "call canceled", "funcName", funcCall.Name)
		} else {
			f.logger.Error(ctx, "call failed", "funcName", funcCall.Name, "error", err)
		}
	}
	f.logger.Debug(ctx, "function call completed", "funcName", funcCall.Name, "duration", time.Since(start), slogx.SensitiveData("result", result))

	return f.createFunctionResult(funcCall.CallID, result, err)
}

// createFunctionResult creates a FunctionResultContent with proper error handling.
// It formats errors into the Result string and preserves the error for re-throwing when limits are exceeded.
func (f *autocall) createFunctionResult(callID string, result any, err error) *message.FunctionResultContent {
	if err == nil && result == nil {
		result = "Success: Function completed."
	}

	// Format errors into Result string for the LLM to see
	if err != nil {
		if f.includeDetailedErrors {
			result = fmt.Sprintf("Error: Function failed. Exception: %v", err)
		} else {
			result = "Error: Function failed."
		}
	}

	return &message.FunctionResultContent{
		CallID: callID,
		Error:  err,
		Result: result,
	}
}

// processFunctionApprovalResponses do the following:
//   - Removes all FunctionApprovalRequestContent and FunctionApprovalResponseContent from msgs.
//   - Recreates FunctionCallContent for any FunctionApprovalResponseContent that haven't been executed yet.
//   - Generates failed FunctionResultContent for any rejected FunctionApprovalResponseContent.
//   - Adds all the new content items to originalMessages and returns them as the pre-invocation history.
func processFunctionApprovalResponses(msgs []*message.Message, toolMsgID, fallbackMsgID string) ([]*message.Message, []*message.Message, []approvalResultWithRequestMessage, error) {
	// Extract any approval responses where we need to execute or reject the function calls.
	// The original messages are also modified to remove all approval requests and responses.
	msgs, approvals, rejections, err := extractAndRemoveApprovalRequestsAndResponses(msgs)
	if err != nil {
		return nil, nil, nil, err
	}
	// Wrap the function call content in message(s).
	preDownstreamCallHistory := convertToFunctionCallContentMessages(append(rejections, approvals...), fallbackMsgID)
	// Generate failed function result contents for any rejected requests and wrap it in a message.
	rejectedFunctionContent := generateRejectedFunctionResults(rejections)
	var rejectedPreDownstreamCallResultsMsgs *message.Message
	if len(rejectedFunctionContent) > 0 {
		rejectedPreDownstreamCallResultsMsgs = &message.Message{
			Role:     message.RoleTool,
			ID:       toolMsgID,
			Contents: rejectedFunctionContent,
		}
	}
	// Add all the FCC that we generated to the pre-downstream-call history so that they can be returned to the caller as part of the next response.
	// Add all the FRC that we generated to the pre-downstream-call history so that they can be returned to the caller as part of the next response.
	// Also, add them into the original messages list so that they are passed to the inner client and can be used to generate a result.
	if rejectedPreDownstreamCallResultsMsgs != nil {
		preDownstreamCallHistory = append(preDownstreamCallHistory, rejectedPreDownstreamCallResultsMsgs)
		msgs = append(msgs, rejectedPreDownstreamCallResultsMsgs)
	}
	return msgs, preDownstreamCallHistory, approvals, nil
}

type approvalResultWithRequestMessage struct {
	Response       *message.FunctionApprovalResponseContent
	RequestMessage *message.Message
}

func extractAndRemoveApprovalRequestsAndResponses(msgs []*message.Message) (out []*message.Message, approvals, rejections []approvalResultWithRequestMessage, err error) {
	var (
		allApprovalRequestsMessages map[string]*message.Message
		approvalRequestCallIDs      map[string]struct{}
		functionResultCallIds       map[string]struct{}
		allApprovalResponses        []*message.FunctionApprovalResponseContent
	)
	// 1st iteration, over all messages and content:
	// - Build a list of all function call ids that are already executed.
	// - Build a list of all function approval requests and responses.
	// - Build a list of the content we want to keep (everything except approval requests and responses) and create a new list of messages for those.
	// - Validate that we have an approval response for each approval request.
	var anyRemoved bool
	for i, msg := range msgs {
		var keptContents []message.Content
		for _, c := range msg.Contents {
			switch c := c.(type) {
			case *message.FunctionApprovalRequestContent:
				// Validation: Capture each call id for each approval request to ensure later we have a matching response.
				if approvalRequestCallIDs == nil {
					approvalRequestCallIDs = make(map[string]struct{})
				}
				approvalRequestCallIDs[c.FunctionCall.CallID] = struct{}{}
				if allApprovalRequestsMessages == nil {
					allApprovalRequestsMessages = make(map[string]*message.Message)
				}
				allApprovalRequestsMessages[c.ID] = msg
			case *message.FunctionApprovalResponseContent:
				// Validation: Remove the call id for each approval response, to check it off the list of requests we need responses for.
				delete(approvalRequestCallIDs, c.FunctionCall.CallID)
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
		callIDs := make([]string, 0, len(approvalRequestCallIDs))
		for callID := range approvalRequestCallIDs {
			callIDs = append(callIDs, callID)
		}
		slices.Sort(callIDs) // Sort for consistent error messages
		return nil, nil, nil, fmt.Errorf("FunctionApprovalRequestContent found with FunctionCall.CallId(s) '%s' that have no matching FunctionApprovalResponseContent", strings.Join(callIDs, "', '"))
	}

	// 2nd iteration, over all approval responses:
	// - Filter out any approval responses that already have a matching function result (i.e. already executed).
	// - Find the matching function approval request for any response (where available).
	// - Split the approval responses into two lists: approved and rejected, with their request messages (where available).
	for _, approvalResponse := range allApprovalResponses {
		if _, found := functionResultCallIds[approvalResponse.FunctionCall.CallID]; found {
			// Skip any approval responses that have already been processed.
			continue
		}
		reqMsg := allApprovalRequestsMessages[approvalResponse.ID]
		// Split the responses into approved and rejected.
		newMsg := approvalResultWithRequestMessage{
			Response:       approvalResponse,
			RequestMessage: reqMsg,
		}
		if approvalResponse.Approved {
			approvals = append(approvals, newMsg)
		} else {
			rejections = append(rejections, newMsg)
		}

	}
	return msgs, approvals, rejections, nil
}

func convertToFunctionCallContentMessages(messages []approvalResultWithRequestMessage, fallbackMessageID string) []*message.Message {
	var currentMsg *message.Message
	var messagesByID map[string]*message.Message
	var messageOrder []*message.Message // Track insertion order since Go maps don't preserve order
	for _, msg := range messages {
		// Don't need to create a dictionary if we already have one or if it's the first iteration.
		shouldCreateMap := messagesByID == nil && currentMsg != nil &&
			// Everywhere we have no RequestMessage we use the fallbackMessageID, so in this case there is only one message.
			!(msg.RequestMessage == nil && currentMsg.ID == fallbackMessageID) &&
			// Where we do have a RequestMessage, we can check if its message id differs from the current one.
			(msg.RequestMessage != nil && currentMsg.ID != msg.RequestMessage.ID)
		if shouldCreateMap {
			// The majority of the time, all FCC would be part of a single message, so no need to create a dictionary for this case.
			// If we are dealing with multiple messages though, we need to keep track of them by their message ID.
			messagesByID = make(map[string]*message.Message)
			messagesByID[currentMsg.ID] = currentMsg
			messageOrder = append(messageOrder, currentMsg)
		}

		// When RequestMessage is nil, use empty string as lookup key (not fallbackMessageID)
		// This matches .NET behavior which uses string.Empty for null RequestMessage
		msgID := ""
		if msg.RequestMessage != nil {
			msgID = msg.RequestMessage.ID
		}

		// Check if we already have a message with this ID
		var foundMsg *message.Message
		if messagesByID != nil {
			foundMsg = messagesByID[msgID]
		} else if currentMsg != nil {
			// If we don't have a map yet, check if currentMsg matches
			// For nil RequestMessage (msgID=""), we need to check if currentMsg also came from nil RequestMessage
			// We can detect this by checking if currentMsg.ID is the fallbackMessageID
			if msgID == "" && currentMsg.ID == fallbackMessageID {
				foundMsg = currentMsg
			} else if msgID != "" && currentMsg.ID == msgID {
				foundMsg = currentMsg
			}
		}

		if foundMsg == nil {
			currentMsg = convertToFunctionCallContentMessage(msg, fallbackMessageID)
			if messagesByID != nil {
				messageOrder = append(messageOrder, currentMsg)
			}
		} else {
			foundMsg.Contents = append(foundMsg.Contents, msg.Response.FunctionCall)
			currentMsg = foundMsg
		}
		if messagesByID != nil {
			// Store with msgID as key, not currentMsg.ID, because we look up by msgID
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

func convertToFunctionCallContentMessage(msg approvalResultWithRequestMessage, fallbackMessageID string) *message.Message {
	var newMsg *message.Message
	if msg.RequestMessage != nil {
		newMsg = msg.RequestMessage.Clone()
	} else {
		newMsg = &message.Message{
			Role: message.RoleAssistant,
		}
	}
	newMsg.Contents = []message.Content{msg.Response.FunctionCall}
	newMsg.ID = cmp.Or(newMsg.ID, fallbackMessageID)
	return newMsg
}

func generateRejectedFunctionResults(rejections []approvalResultWithRequestMessage) []message.Content {
	if len(rejections) == 0 {
		return nil
	}
	contents := make([]message.Content, 0, len(rejections))
	for _, rej := range rejections {
		resultContent := &message.FunctionResultContent{
			CallID: rej.Response.FunctionCall.CallID,
			Result: "Error: Tool call invocation was rejected by user.",
		}
		contents = append(contents, resultContent)
	}
	return contents
}
