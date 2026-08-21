// Copyright (c) Microsoft. All rights reserved.

package loop

import (
	"context"
	"errors"
	"strings"
)

// CompletionMarkerConfig configures a completion-marker evaluator.
type CompletionMarkerConfig struct {
	// Marker is the completion marker that stops the loop when present in the
	// latest response text.
	Marker string

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

// NewCompletionMarkerEvaluator creates an evaluator that waits for the
// configured marker in the latest response text.
func NewCompletionMarkerEvaluator(config CompletionMarkerConfig) *CompletionMarkerEvaluator {
	marker := strings.TrimSpace(config.Marker)
	if marker == "" {
		panic("loop: completion marker cannot be empty")
	}
	template := defaultCompletionMarkerFeedbackTemplate
	if config.FeedbackMessageTemplate != "" {
		template = config.FeedbackMessageTemplate
	}
	return &CompletionMarkerEvaluator{
		completionMarker:        marker,
		feedbackMessageTemplate: prepareCompletionMarkerFeedbackTemplate(template, marker),
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
	responseText := loop.LastResponse.String()
	if strings.Contains(responseText, e.completionMarker) {
		return Stop(), nil
	}
	return Continue(formatCompletionMarkerFeedback(e.feedbackMessageTemplate, responseText)), nil
}

func prepareCompletionMarkerFeedbackTemplate(template, marker string) string {
	return strings.ReplaceAll(template, completionMarkerPlaceholder, marker)
}

func formatCompletionMarkerFeedback(template, responseText string) string {
	return strings.ReplaceAll(template, lastResponsePlaceholder, responseText)
}
