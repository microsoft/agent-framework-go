// Copyright (c) Microsoft. All rights reserved.

package telemetry

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	featureMaskDisabledEnvVar = "AGENT_FRAMEWORK_FEATURE_MASK_DISABLED"
	featureRegistryVersion    = 1
)

// FeatureUsage provides process-wide tracking for Agent Framework feature usage.
//
// It supports framework integrations and is not intended for direct use by applications.
var FeatureUsage featureUsage

type featureUsage struct{}

var (
	featureUsageLowMask  atomic.Uint64
	featureUsageHighMask atomic.Uint64
	featureMaskDisabled  = sync.OnceValue(func() bool {
		switch strings.ToLower(os.Getenv(featureMaskDisabledEnvVar)) {
		case "true", "1":
			return true
		default:
			return false
		}
	})
)

// MarkUsed marks a registered Agent Framework feature as used in the current process.
func (featureUsage) MarkUsed(index int) {
	if featureMaskDisabled() {
		return
	}
	if index < 0 || index >= 128 {
		panic(fmt.Sprintf("feature index %d must be in the range 0 through 127", index))
	}

	var mask *atomic.Uint64
	bit := uint64(1) << uint(index&63)
	if index < 64 {
		mask = &featureUsageLowMask
	} else {
		mask = &featureUsageHighMask
	}
	atomicOr(mask, bit)
}

// ApplyToUserAgent appends or removes the current feature-usage token from a User-Agent value.
func (featureUsage) ApplyToUserAgent(userAgent string, includeFeatureToken ...bool) string {
	include := true
	if len(includeFeatureToken) > 0 {
		include = includeFeatureToken[0]
	}

	baseUserAgent := removeFeatureComments(userAgent)
	token := ""
	if include {
		token = currentFeatureToken()
	}
	if token == "" {
		return baseUserAgent
	}
	if baseUserAgent == "" {
		return "(feat=" + token + ")"
	}
	return baseUserAgent + " (feat=" + token + ")"
}

func currentFeatureToken() string {
	if featureMaskDisabled() {
		return ""
	}

	low := featureUsageLowMask.Load()
	high := featureUsageHighMask.Load()
	if low == 0 && high == 0 {
		return ""
	}
	if high == 0 {
		return fmt.Sprintf("v%d.%x", featureRegistryVersion, low)
	}
	return fmt.Sprintf("v%d.%x%016x", featureRegistryVersion, high, low)
}

func atomicOr(target *atomic.Uint64, bit uint64) {
	for {
		current := target.Load()
		if current&bit != 0 {
			return
		}
		if target.CompareAndSwap(current, current|bit) {
			return
		}
	}
}

func removeFeatureComments(userAgent string) string {
	start, end, ok := findFeatureComment(userAgent, 0)
	if !ok {
		return userAgent
	}

	var b strings.Builder
	b.Grow(len(userAgent))
	copyFrom := 0
	for ok {
		removeFrom := start
		removeThrough := end

		if removeFrom > copyFrom && isWhitespace(userAgent[removeFrom-1]) {
			removeFrom--
		} else if removeFrom == copyFrom && removeThrough < len(userAgent) && isWhitespace(userAgent[removeThrough]) {
			removeThrough++
		}

		b.WriteString(userAgent[copyFrom:removeFrom])
		copyFrom = removeThrough
		start, end, ok = findFeatureComment(userAgent, end)
	}

	b.WriteString(userAgent[copyFrom:])
	return b.String()
}

func findFeatureComment(userAgent string, searchFrom int) (start int, end int, ok bool) {
	const prefix = "(feat=v"

	for {
		index := strings.Index(userAgent[searchFrom:], prefix)
		if index < 0 {
			return -1, -1, false
		}
		start = searchFrom + index
		if start > 0 && !isWhitespace(userAgent[start-1]) {
			searchFrom = start + len(prefix)
			continue
		}

		cursor := start + len(prefix)
		versionStart := cursor
		for cursor < len(userAgent) && userAgent[cursor] >= '0' && userAgent[cursor] <= '9' {
			cursor++
		}
		if cursor == versionStart || cursor >= len(userAgent) || userAgent[cursor] != '.' {
			searchFrom = start + len(prefix)
			continue
		}

		cursor++
		maskStart := cursor
		for cursor < len(userAgent) && isHexDigit(userAgent[cursor]) {
			cursor++
		}
		if cursor == maskStart || cursor >= len(userAgent) || userAgent[cursor] != ')' {
			searchFrom = start + len(prefix)
			continue
		}

		end = cursor + 1
		if end == len(userAgent) || isWhitespace(userAgent[end]) {
			return start, end, true
		}

		searchFrom = start + len(prefix)
	}
}

func isHexDigit(value byte) bool {
	return value >= '0' && value <= '9' ||
		value >= 'a' && value <= 'f' ||
		value >= 'A' && value <= 'F'
}

func isWhitespace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\n' || value == '\r' || value == '\f' || value == '\v'
}
