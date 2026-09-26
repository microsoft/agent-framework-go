// Copyright (c) Microsoft. All rights reserved.

package loop

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
)

const (
	// AIJudgeDoneMarker is the text-fallback marker the judge is asked to emit
	// (for judges that do not honor structured output) when the original request
	// has been fully addressed.
	AIJudgeDoneMarker = "VERDICT: DONE"
	// AIJudgeMoreMarker is the text-fallback marker for "more work required". It
	// deliberately does not overlap AIJudgeDoneMarker and wins when the verdict
	// is ambiguous or absent, so the loop keeps running rather than stopping on
	// an incomplete answer.
	AIJudgeMoreMarker = "VERDICT: MORE"

	// AIJudgeCriteriaPlaceholder in the judge instructions is replaced with the
	// rendered criteria (or removed when none are supplied).
	AIJudgeCriteriaPlaceholder = "{criteria}"
	// AIJudgeGapAnalysisPlaceholder in the feedback template is replaced with the
	// judge's gap analysis.
	AIJudgeGapAnalysisPlaceholder = "{gap_analysis}"

	aiJudgeUnknownGapAnalysis = "<unknown>"

	// DefaultAIJudgeInstructions are the default system instructions for the judge.
	DefaultAIJudgeInstructions = "You are an evaluator. You are given a user's original request and an agent's latest response. " +
		"Decide whether the agent has fully addressed the original request. " +
		"Set 'answered' to true if the request has been fully addressed, or false if more work is still required. " +
		"When 'answered' is false, use 'gapAnalysis' to explain what is still missing or what work remains. " +
		"If you cannot return structured output, reply with " + AIJudgeDoneMarker + " when the request has been fully " +
		"addressed, or " + AIJudgeMoreMarker + " when more work is still required." + AIJudgeCriteriaPlaceholder

	// DefaultAIJudgeFeedbackTemplate is the default feedback template used when
	// the request is not yet answered.
	DefaultAIJudgeFeedbackTemplate = "Your previous response did not fully address the original request. " +
		"The following is still missing or incomplete: " + AIJudgeGapAnalysisPlaceholder + " " +
		"Please continue and fully address the original request."
)

// JudgeVerdict is the structured verdict an AI judge returns.
type JudgeVerdict struct {
	// Answered is true when the judge decided the original request was fully addressed.
	Answered bool `json:"answered"`
	// GapAnalysis explains what is still missing when Answered is false.
	GapAnalysis string `json:"gapAnalysis"`
}

// AIJudgeConfig configures [NewAIJudgeEvaluator].
type AIJudgeConfig struct {
	// Instructions overrides the judge system instructions. Any occurrence of
	// [AIJudgeCriteriaPlaceholder] is replaced with the rendered Criteria. When
	// nil, [DefaultAIJudgeInstructions] is used.
	Instructions *string

	// Criteria are bespoke standards the response must satisfy, appended to the
	// instructions at [AIJudgeCriteriaPlaceholder].
	Criteria []string

	// FeedbackMessageTemplate overrides the feedback produced when the request is
	// not yet answered. [AIJudgeGapAnalysisPlaceholder] is replaced with the
	// judge's gap analysis. When nil, [DefaultAIJudgeFeedbackTemplate] is used.
	FeedbackMessageTemplate *string
}

// AIJudgeEvaluator uses a separate judge agent to decide whether the user's
// original request has been fully addressed, continuing the loop (with the
// judge's gap analysis as feedback) while the answer is "no".
//
// Security: the judge agent is sent the original request and the agent's latest
// response on every iteration, both of which may contain sensitive or untrusted
// content. A compromised judge endpoint could exfiltrate that data or return a
// manipulated verdict that steers the agent via indirect prompt injection. Only
// use a judge you trust as much as the primary model, and prefer a stricter
// [Config.MaxIterations] since LLM-judged loops are costly and probabilistic.
type AIJudgeEvaluator struct {
	judge                   *agent.Agent
	instructions            string
	feedbackMessageTemplate string
}

// NewAIJudgeEvaluator creates an evaluator that queries the judge agent after
// each iteration. It panics if judge is nil.
func NewAIJudgeEvaluator(judge *agent.Agent, config AIJudgeConfig) *AIJudgeEvaluator {
	if judge == nil {
		panic("loop: judge agent cannot be nil")
	}
	instructions := DefaultAIJudgeInstructions
	if config.Instructions != nil {
		instructions = *config.Instructions
	}
	instructions = strings.ReplaceAll(instructions, AIJudgeCriteriaPlaceholder, renderJudgeCriteria(config.Criteria))
	feedback := DefaultAIJudgeFeedbackTemplate
	if config.FeedbackMessageTemplate != nil {
		feedback = *config.FeedbackMessageTemplate
	}
	return &AIJudgeEvaluator{
		judge:                   judge,
		instructions:            instructions,
		feedbackMessageTemplate: feedback,
	}
}

// Evaluate implements Evaluator.
func (e *AIJudgeEvaluator) Evaluate(ctx context.Context, loop *Context) (Evaluation, error) {
	if loop == nil {
		return Stop(), errors.New("loop: context cannot be nil")
	}
	if loop.LastResponse == nil {
		return Stop(), errors.New("loop: last response cannot be nil")
	}

	// Build the judge's user message from the original request contents (so
	// non-text content is preserved rather than flattened) framed by header text,
	// followed by the agent's latest response.
	contents := message.Contents{&message.TextContent{Text: "# Has the original request been fully addressed?\n\n## Original request:\n"}}
	for _, m := range loop.InitialMessages {
		contents = append(contents, m.Contents...)
	}
	contents = append(contents, &message.TextContent{Text: "\n\n## Agent's latest response:\n" + loop.LastResponse.String()})

	resp, err := e.judge.Run(ctx, []*message.Message{{Role: message.RoleUser, Contents: contents}}, agent.WithInstructions(e.instructions)).Collect()
	if err != nil {
		return Stop(), err
	}

	answered, gapAnalysis := parseJudgeVerdict(resp.String())
	if answered {
		return Stop(), nil
	}
	return Continue(strings.ReplaceAll(e.feedbackMessageTemplate, AIJudgeGapAnalysisPlaceholder, gapAnalysis)), nil
}

// parseJudgeVerdict prefers a structured JudgeVerdict, falling back to the
// text markers. AIJudgeMoreMarker wins when ambiguous or absent so the loop
// keeps running rather than stopping on an incomplete answer.
func parseJudgeVerdict(text string) (answered bool, gapAnalysis string) {
	gapAnalysis = aiJudgeUnknownGapAnalysis
	if verdict, ok := extractJudgeVerdict(text); ok {
		if strings.TrimSpace(verdict.GapAnalysis) != "" {
			gapAnalysis = verdict.GapAnalysis
		}
		return verdict.Answered, gapAnalysis
	}
	upper := strings.ToUpper(text)
	answered = !strings.Contains(upper, AIJudgeMoreMarker) && strings.Contains(upper, AIJudgeDoneMarker)
	return answered, gapAnalysis
}

// extractJudgeVerdict pulls a JudgeVerdict out of the response text, tolerating
// surrounding prose or code fences. It only succeeds when the JSON object
// actually carries an "answered" key, so plain text does not decode to a
// spurious zero-value verdict.
func extractJudgeVerdict(text string) (JudgeVerdict, bool) {
	start := strings.Index(text, "{")
	end := strings.LastIndex(text, "}")
	if start < 0 || end <= start {
		return JudgeVerdict{}, false
	}
	raw := []byte(text[start : end+1])
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return JudgeVerdict{}, false
	}
	if _, ok := probe["answered"]; !ok {
		return JudgeVerdict{}, false
	}
	var verdict JudgeVerdict
	if err := json.Unmarshal(raw, &verdict); err != nil {
		return JudgeVerdict{}, false
	}
	return verdict, true
}

func renderJudgeCriteria(criteria []string) string {
	var b strings.Builder
	for _, c := range criteria {
		if strings.TrimSpace(c) != "" {
			b.WriteString("\n- ")
			b.WriteString(c)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n\nThe response must satisfy all of the following criteria:" + b.String()
}

// CompletionMarkerConfig configures a completion-marker evaluator.
type CompletionMarkerConfig struct {
	// Marker is the completion marker that stops the loop when present in the
	// latest response text.
	Marker string

	// FeedbackMessageTemplate is used when the marker is absent. When nil, the
	// default template is used. The
	// completionMarkerPlaceholder token is replaced when the evaluator is
	// created, and lastResponsePlaceholder is replaced on each evaluation.
	FeedbackMessageTemplate *string
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
	if config.FeedbackMessageTemplate != nil {
		template = *config.FeedbackMessageTemplate
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
