// Copyright (c) Microsoft. All rights reserved.

package telemetry

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"
)

// Feature indexes match the .NET version-1 registry. Only supported capabilities
// registered there are tracked; indexes must not be repurposed for Go-only features.
const (
	FeatureAgent                   = 0
	FeatureWorkflow                = 2
	FeatureToolApproval            = 3
	FeatureInMemoryHistoryProvider = 13
	FeatureCompactionProvider      = 9
	FeatureSkillsProvider          = 8
	FeatureFileSkillsSource        = 15
	FeatureInMemorySkillsSource    = 16
	FeatureMCP                     = 14
	FeatureTodoProvider            = 10
	FeatureAgentModeProvider       = 11
	FeatureSequentialOrchestration = 32
	FeatureConcurrentOrchestration = 33
	FeatureGroupChatOrchestration  = 34
	FeatureFoundryChatClient       = 48
	FeatureFoundryAgent            = 49
	FeatureFoundryMemory           = 50
	FeatureOpenAI                  = 54
	FeatureAnthropic               = 55
	FeatureGitHubCopilot           = 57
	FeatureA2A                     = 62
	FeatureHostingAGUI             = 63
	FeatureShell                   = 69
	FeatureHostingA2A              = 73
)

const featureMaskDisabledEnvVar = "AGENT_FRAMEWORK_FEATURE_MASK_DISABLED"

var (
	openAIHostSuffixes = [...]string{
		"cognitiveservices.azure.com",
		"openai.azure.com",
		"services.ai.azure.com",
	}
	foundryHostSuffixes = [...]string{
		"services.ai.azure.com",
		"inference.ai.azure.com",
	}
	featureLow   atomic.Uint64
	featureHigh  atomic.Uint64
	cachedToken  atomic.Pointer[featureToken]
	featureRegex = regexp.MustCompile(`^\(feat=v[0-9]+\.[0-9a-fA-F]+\)\z`)
)

type featureToken struct {
	low, high uint64
	value     string
}

var featureMaskDisabled = sync.OnceValue(func() bool {
	value := os.Getenv(featureMaskDisabledEnvVar)
	return strings.EqualFold(value, "true") || value == "1"
})

// MarkUsed records a feature for the lifetime of the process. It is concurrency-safe
// and idempotent. It panics for indexes outside 0 through 127, unless tracking is
// disabled by AGENT_FRAMEWORK_FEATURE_MASK_DISABLED=true or 1.
func MarkUsed(index int) {
	if featureMaskDisabled() {
		return
	}
	if index < 0 || index >= 128 {
		panic(fmt.Sprintf("feature index %d must be in the range 0 through 127", index))
	}
	bit := uint64(1) << (index & 63)
	if index < 64 {
		featureLow.Or(bit)
	} else {
		featureHigh.Or(bit)
	}
}

// ApplyToUserAgent refreshes the feature comment while preserving unrelated bytes.
// Valid existing comments are removed when the mask is empty, disabled, or excluded.
// Callers must approve the destination independently before including the token.
func ApplyToUserAgent(userAgent string, includeFeatureToken bool) string {
	base := removeFeatureComments(userAgent)
	if !includeFeatureToken {
		return base
	}
	token := currentFeatureToken()
	if token == "" {
		return base
	}
	if base == "" {
		return "(feat=" + token + ")"
	}
	return base + " (feat=" + token + ")"
}

func currentFeatureToken() string {
	if featureMaskDisabled() {
		return ""
	}
	low, high := featureLow.Load(), featureHigh.Load()
	if low == 0 && high == 0 {
		return ""
	}
	if cached := cachedToken.Load(); cached != nil && cached.low == low && cached.high == high {
		return cached.value
	}
	var token string
	if high == 0 {
		token = fmt.Sprintf("v1.%x", low)
	} else {
		token = fmt.Sprintf("v1.%x%016x", high, low)
	}
	cachedToken.Store(&featureToken{low: low, high: high, value: token})
	return token
}

func removeFeatureComments(userAgent string) string {
	var result strings.Builder
	copyFrom := 0
	for _, comment := range topLevelFeatureComments(userAgent) {
		start, end := comment[0], comment[1]
		before, beforeSize := utf8.DecodeLastRuneInString(userAgent[:start])
		after, afterSize := utf8.DecodeRuneInString(userAgent[end:])
		if copyFrom == 0 {
			result.Grow(len(userAgent))
		}
		removeFrom, removeThrough := start, end
		if start > copyFrom && unicode.IsSpace(before) {
			removeFrom -= beforeSize
		} else if start == copyFrom && end < len(userAgent) && unicode.IsSpace(after) {
			removeThrough += afterSize
		}
		result.WriteString(userAgent[copyFrom:removeFrom])
		copyFrom = removeThrough
	}
	if copyFrom == 0 {
		return userAgent
	}
	result.WriteString(userAgent[copyFrom:])
	return result.String()
}

func topLevelFeatureComments(userAgent string) [][2]int {
	var comments [][2]int
	depth := 0
	start := -1
	for i := 0; i < len(userAgent); i++ {
		switch userAgent[i] {
		case '\\':
			if depth > 0 && i+1 < len(userAgent) {
				i++
			}
		case '(':
			if depth == 0 {
				start = i
			}
			depth++
		case ')':
			if depth == 0 {
				continue
			}
			depth--
			if depth != 0 {
				continue
			}
			end := i + 1
			before, _ := utf8.DecodeLastRuneInString(userAgent[:start])
			after, _ := utf8.DecodeRuneInString(userAgent[end:])
			if (start == 0 || unicode.IsSpace(before)) &&
				(end == len(userAgent) || unicode.IsSpace(after)) &&
				featureRegex.MatchString(userAgent[start:end]) {
				comments = append(comments, [2]int{start, end})
			}
			start = -1
		}
	}
	return comments
}

// ApplyToHTTPRequest adds the framework identity and refreshes the feature comment
// for approved Azure origins. Only origin-guarded OpenAI/Foundry clients may use
// this helper; their transports must also enforce the origin on redirects.
func ApplyToHTTPRequest(req *http.Request) {
	req.Header = PrependAgentFrameworkToHTTPHeader(req.Header)
	applyFeatureUsageToHTTPRequest(req, openAIHostSuffixes[:])
}

// ApplyToFoundryHTTPRequest adds the framework identity and refreshes the feature
// comment for approved Foundry origins. The transport must enforce redirect origins.
func ApplyToFoundryHTTPRequest(req *http.Request) {
	req.Header = PrependAgentFrameworkToHTTPHeader(req.Header)
	applyFeatureUsageToHTTPRequest(req, foundryHostSuffixes[:])
}

func applyFeatureUsageToHTTPRequest(req *http.Request, approvedHostSuffixes []string) {
	headers := req.Header
	current := headers.Get(userAgentKey)
	updated := ApplyToUserAgent(current, userAgentTelemetryEnabled() && approvedFeatureOrigin(req.URL, approvedHostSuffixes))
	if current != updated {
		if updated == "" {
			headers.Del(userAgentKey)
		} else {
			if headers == nil {
				headers = make(http.Header)
			}
			headers.Set(userAgentKey, updated)
		}
	}
	req.Header = headers
}

func approvedFeatureOrigin(destination *url.URL, approvedHostSuffixes []string) bool {
	if destination == nil || !destination.IsAbs() || !strings.EqualFold(destination.Scheme, "https") {
		return false
	}
	host := strings.TrimRight(strings.ToLower(destination.Hostname()), ".")
	for _, suffix := range approvedHostSuffixes {
		if host == suffix || strings.HasSuffix(host, "."+suffix) {
			return true
		}
	}
	return false
}
