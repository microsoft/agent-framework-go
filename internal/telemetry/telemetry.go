// Copyright (c) Microsoft. All rights reserved.

package telemetry

import (
	"context"
	"maps"
	"net/http"
	"net/url"
	"os"
	"runtime/debug"
	"slices"
	"strings"
	"sync"

	"github.com/microsoft/agent-framework-go/agent"
	publictelemetry "github.com/microsoft/agent-framework-go/telemetry"
)

const (
	modulePath                       = "github.com/microsoft/agent-framework-go"
	userAgentTelemetryDisabledEnvVar = "AGENT_FRAMEWORK_USER_AGENT_DISABLED"
	userAgentKey                     = "User-Agent"
	httpUserAgent                    = "agent-framework-go"
	foundryHostingEnvVar             = "FOUNDRY_HOSTING_ENVIRONMENT"
	hostedUserAgentPrefix            = "foundry-hosting"
)

type BaseUserAgentScope int

const (
	BaseUserAgentScopeApprovedOrigins BaseUserAgentScope = iota
	BaseUserAgentScopeAllRequests
)

type FeatureUsageConfig struct {
	Index                int
	BaseUserAgentScope   BaseUserAgentScope
	ApprovedHostSuffixes []string
}

type featureUsageOpt struct{ FeatureUsageConfig }

func (o featureUsageOpt) MAFValue() any { return o.FeatureUsageConfig }

func WithFeatureUsage(config FeatureUsageConfig) agent.Option {
	config.ApprovedHostSuffixes = slices.Clone(config.ApprovedHostSuffixes)
	return featureUsageOpt{config}
}

var (
	version                 = detectVersion()
	agentFrameworkUserAgent = httpUserAgent + "/" + version
	userAgentPrefixes       = map[string]struct{}{}
)

type featureUsageContextKey struct{}

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

func ConfigureRequestContext(ctx context.Context, options []agent.Option) context.Context {
	config, ok := agent.GetOption(options, WithFeatureUsage)
	if !ok {
		return ctx
	}
	publictelemetry.FeatureUsage.MarkUsed(config.Index)
	return context.WithValue(ctx, featureUsageContextKey{}, config)
}

func ApplyToRequest(req *http.Request) {
	if req == nil || !userAgentTelemetryEnabled() {
		return
	}
	if req.Header == nil {
		req.Header = http.Header{}
	}

	config, _ := req.Context().Value(featureUsageContextKey{}).(FeatureUsageConfig)
	updateHTTPUserAgent(req.Header, applyRequestUserAgent(req.Header.Get(userAgentKey), config, req.URL))
}

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
	existing = publictelemetry.FeatureUsage.ApplyToUserAgent(existing)
	if existing == "" {
		return userAgent()
	}
	return userAgent() + " " + existing
}

func applyRequestUserAgent(existing string, config FeatureUsageConfig, requestURL *url.URL) string {
	approved := isApprovedOrigin(requestURL, config.ApprovedHostSuffixes)
	updated := publictelemetry.FeatureUsage.ApplyToUserAgent(existing, approved)

	if config.BaseUserAgentScope == BaseUserAgentScopeAllRequests || approved {
		if updated == "" {
			return userAgent()
		}
		return userAgent() + " " + updated
	}

	return updated
}

func updateHTTPUserAgent(headers http.Header, value string) {
	if value == "" {
		headers.Del(userAgentKey)
		return
	}
	headers.Set(userAgentKey, value)
}

func isApprovedOrigin(requestURL *url.URL, approvedHostSuffixes []string) bool {
	if requestURL == nil || !strings.EqualFold(requestURL.Scheme, "https") {
		return false
	}

	host := strings.TrimRight(requestURL.Hostname(), ".")
	if host == "" {
		return false
	}
	for _, suffix := range approvedHostSuffixes {
		if strings.EqualFold(host, suffix) || strings.HasSuffix(strings.ToLower(host), "."+strings.ToLower(suffix)) {
			return true
		}
	}
	return false
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
