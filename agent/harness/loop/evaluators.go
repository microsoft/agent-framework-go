// Copyright (c) Microsoft. All rights reserved.

package loop

import (
	"context"
	"errors"
	"strings"
)

// CompletionMarkerConfig configures a completion-marker evaluator.
type CompletionMarkerConfig struct {
	// FeedbackMessageTemplate is used when the marker is absent. The
	// completionMarkerPlaceholder token is replaced when the evaluator is
	// created, and lastResponsePlaceholder is replaced on each evaluation.
	FeedbackMessageTemplate string
}

// CompletionMarkerEvaluator stops the loop once a marker appears in the latest
// response text, otherwise it asks the agent to continue.
type CompletionMarkerEvaluator struct {
	completionMarker        string
	feedbackMessageTemplate string
}

// NewCompletionMarkerEvaluator creates an evaluator that waits for marker in
// the latest response text.
func NewCompletionMarkerEvaluator(marker string, config CompletionMarkerConfig) *CompletionMarkerEvaluator {
	template := defaultCompletionMarkerFeedbackTemplate
	if config.FeedbackMessageTemplate != "" {
		template = config.FeedbackMessageTemplate
	}
	template = strings.ReplaceAll(template, completionMarkerPlaceholder, strings.TrimSpace(marker))
	return &CompletionMarkerEvaluator{
		completionMarker:        marker,
		feedbackMessageTemplate: template,
	}
}

// Evaluate implements Evaluator.
func (e *CompletionMarkerEvaluator) Evaluate(_ context.Context, loop *Context) (Evaluation, error) {
	if loop == nil {
		return Stop(), errors.New("loop: context cannot be nil")
	}
	if loop.LastResponse == nil {
		return Stop(), errors.New("loop: last response cannot be nil")
	}
	marker := strings.TrimSpace(e.completionMarker)
	if marker == "" {
		return Stop(), errors.New("loop: completion marker cannot be empty")
	}
	responseText := loop.LastResponse.String()
	if strings.HasSuffix(strings.TrimSpace(responseText), marker) {
		return Stop(), nil
	}
	feedback := strings.ReplaceAll(e.feedbackMessageTemplate, lastResponsePlaceholder, responseText)
	return Continue(feedback), nil
}
