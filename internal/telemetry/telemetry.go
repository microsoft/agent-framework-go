// Copyright (c) Microsoft. All rights reserved.

package telemetry

import (
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
	userAgent := userAgent()
	if existing := headers[userAgentKey]; existing != "" {
		headers[userAgentKey] = userAgent + " " + existing
	} else {
		headers[userAgentKey] = userAgent
	}
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
	userAgent := userAgent()
	if existing := headers.Get(userAgentKey); existing != "" {
		headers.Set(userAgentKey, userAgent+" "+existing)
	} else {
		headers.Set(userAgentKey, userAgent)
	}
	return headers
}

func userAgentPrefixList() []string {
	prefixes := make([]string, 0, len(userAgentPrefixes))
	for prefix := range userAgentPrefixes {
		prefixes = append(prefixes, prefix)
	}
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
