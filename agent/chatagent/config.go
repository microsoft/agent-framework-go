// Copyright (c) Microsoft. All rights reserved.

package chatagent

import (
	"github.com/microsoft/agent-framework-go/agent/agentopt"
)

type (
	conversationIDOpt         string
	temperatureOpt            float64
	topPOpt                   float64
	maxOutputTokensOpt        int64
	presencePenaltyOpt        float64
	frequencyPenaltyOpt       float64
	seedOpt                   int64
	allowMultipleToolCallsOpt bool
	stopSequenceOpt           []string
	modelOpt                  string
)

func (conversationIDOpt) CreateSessionOption() {}

func (temperatureOpt) RunOption()            {}
func (topPOpt) RunOption()                   {}
func (maxOutputTokensOpt) RunOption()        {}
func (presencePenaltyOpt) RunOption()        {}
func (frequencyPenaltyOpt) RunOption()       {}
func (seedOpt) RunOption()                   {}
func (allowMultipleToolCallsOpt) RunOption() {}
func (stopSequenceOpt) RunOption()           {}
func (modelOpt) RunOption()                  {}

func (o conversationIDOpt) Value() any         { return string(o) }
func (o temperatureOpt) Value() any            { return float64(o) }
func (o topPOpt) Value() any                   { return float64(o) }
func (o maxOutputTokensOpt) Value() any        { return int64(o) }
func (o presencePenaltyOpt) Value() any        { return float64(o) }
func (o frequencyPenaltyOpt) Value() any       { return float64(o) }
func (o seedOpt) Value() any                   { return int64(o) }
func (o allowMultipleToolCallsOpt) Value() any { return bool(o) }
func (o stopSequenceOpt) Value() any           { return []string(o) }
func (o modelOpt) Value() any                  { return string(o) }

func ConversationID(conversationID string) agentopt.CreateSessionOption {
	return conversationIDOpt(conversationID)
}

func Temperature(temperature float64) agentopt.RunOption {
	return temperatureOpt(temperature)
}

func TopP(topP float64) agentopt.RunOption {
	return topPOpt(topP)
}

func MaxOutputTokens(maxTokens int64) agentopt.RunOption {
	return maxOutputTokensOpt(maxTokens)
}

func PresencePenalty(penalty float64) agentopt.RunOption {
	return presencePenaltyOpt(penalty)
}

func FrequencyPenalty(penalty float64) agentopt.RunOption {
	return frequencyPenaltyOpt(penalty)
}

func Seed(seed int64) agentopt.RunOption {
	return seedOpt(seed)
}

func AllowMultipleToolCalls(allow bool) agentopt.RunOption {
	return allowMultipleToolCallsOpt(allow)
}

func StopSequences(sequences []string) agentopt.RunOption {
	return stopSequenceOpt(sequences)
}

func Model(model string) agentopt.RunOption {
	return modelOpt(model)
}
