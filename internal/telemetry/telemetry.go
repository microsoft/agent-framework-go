// Copyright (c) Microsoft. All rights reserved.

package telemetry

import (
	"maps"
	"net/http"
	"os"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
)

const (
	modulePath                       = "github.com/microsoft/agent-framework-go"
	userAgentTelemetryDisabledEnvVar = "AGENT_FRAMEWORK_USER_AGENT_DISABLED"
	userAgentKey                     = "User-Agent"
	httpUserAgent                    = "agent-framework-go"
	foundryHostingEnvVar             = "FOUNDRY_HOSTING_ENVIRONMENT"
	hostedUserAgentPrefix            = "foundry-hosting"
)

var (
	version                 = detectVersion()
	agentFrameworkUserAgent = httpUserAgent + "/" + version
	userAgentPrefixes       = map[string]struct{}{}
)

var userAgentTelemetryEnabled = sync.OnceValue(func() bool {
	switch strings.ToLower(os.Getenv(userAgentTelemetryDisabledEnvVar)) {
	case "true", "1":
		return false
	default:
		return true
	}
})

var userAgent = sync.OnceValue(func() string {
	if os.Getenv(foundryHostingEnvVar) != "" {
		if hostedUserAgentPrefix != "" {
			userAgentPrefixes[hostedUserAgentPrefix] = struct{}{}
		}
	}
	prefixes := userAgentPrefixList()
	if len(prefixes) == 0 {
		return agentFrameworkUserAgent
	}
	return strings.Join(prefixes, "/") + "/" + agentFrameworkUserAgent
})

// PrependAgentFrameworkToUserAgent prepends the Agent Framework user-agent value to headers.
func PrependAgentFrameworkToUserAgent(headers map[string]string) map[string]string {
	if !userAgentTelemetryEnabled() {
		return headers
	}
	if headers == nil {
		headers = map[string]string{}
	}
	headers[userAgentKey] = prependUserAgent(headers[userAgentKey])
	return headers
}

// PrependAgentFrameworkToHTTPHeader prepends the Agent Framework user-agent value to an http.Header.
func PrependAgentFrameworkToHTTPHeader(headers http.Header) http.Header {
	if !userAgentTelemetryEnabled() {
		return headers
	}
	if headers == nil {
		headers = http.Header{}
	}
	headers.Set(userAgentKey, prependUserAgent(headers.Get(userAgentKey)))
	return headers
}

func prependUserAgent(existing string) string {
	frameworkUserAgent := userAgent()
	products := strings.Fields(existing)
	frameworkProductCount := 0
	hasCurrentFrameworkProduct := false
	for _, product := range products {
		if isAgentFrameworkUserAgent(product) {
			frameworkProductCount++
			hasCurrentFrameworkProduct = hasCurrentFrameworkProduct || product == frameworkUserAgent
		}
	}
	if frameworkProductCount == 1 && hasCurrentFrameworkProduct {
		return existing
	}
	if frameworkProductCount > 0 {
		normalized := make([]string, 0, len(products)-frameworkProductCount+1)
		frameworkProductAdded := false
		for _, product := range products {
			if isAgentFrameworkUserAgent(product) {
				if !frameworkProductAdded {
					normalized = append(normalized, frameworkUserAgent)
					frameworkProductAdded = true
				}
				continue
			}
			normalized = append(normalized, product)
		}
		return strings.Join(normalized, " ")
	}
	if existing == "" {
		return frameworkUserAgent
	}
	return frameworkUserAgent + " " + existing
}

func isAgentFrameworkUserAgent(product string) bool {
	return strings.HasPrefix(product, httpUserAgent+"/") ||
		strings.Contains(product, "/"+httpUserAgent+"/")
}

func userAgentPrefixList() []string {
	prefixes := slices.Collect(maps.Keys(userAgentPrefixes))
	slices.Sort(prefixes)
	return prefixes
}

func detectVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "v0.0.0"
	}
	if info.Main.Path == modulePath && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	for _, dep := range info.Deps {
		if dep.Path == modulePath && dep.Version != "" && dep.Version != "(devel)" {
			return dep.Version
		}
	}
	return "v0.0.0"
}
