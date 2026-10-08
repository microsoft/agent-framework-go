// Copyright (c) Microsoft. All rights reserved.

package telemetry_test

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/internal/telemetry"
)

const helperProcessEnv = "AGENT_FRAMEWORK_TELEMETRY_TEST_HELPER"

const (
	foundryHostingEnvVar             = "FOUNDRY_HOSTING_ENVIRONMENT"
	userAgentKey                     = "User-Agent"
	userAgentTelemetryDisabledEnvVar = "AGENT_FRAMEWORK_USER_AGENT_DISABLED"
	featureMaskDisabledEnvVar        = "AGENT_FRAMEWORK_FEATURE_MASK_DISABLED"
	featureUserAgentEnvVar           = "AGENT_FRAMEWORK_TEST_USER_AGENT"
)

func TestVersionHasLeadingV(t *testing.T) {
	got := runHelper(t, "user-agent", foundryHostingEnvVar+"=", userAgentTelemetryDisabledEnvVar+"=")
	if !strings.HasPrefix(got, "agent-framework-go/v") {
		t.Fatalf("User-Agent = %q, want agent-framework-go/v...", got)
	}
}

func TestPrependAgentFrameworkToUserAgent(t *testing.T) {
	got := runHelper(t, "prepend-map", userAgentTelemetryDisabledEnvVar+"=")
	want := runHelper(t, "user-agent", foundryHostingEnvVar+"=", userAgentTelemetryDisabledEnvVar+"=") + " my-app/1.0"
	if got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
}

func TestPrependAgentFrameworkToUserAgentIsIdempotent(t *testing.T) {
	got := runHelper(t, "prepend-map-twice", userAgentTelemetryDisabledEnvVar+"=")
	want := runHelper(t, "user-agent", foundryHostingEnvVar+"=", userAgentTelemetryDisabledEnvVar+"=") + " my-app/1.0"
	if got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
}

func TestPrependAgentFrameworkToUserAgentWithNilHeaders(t *testing.T) {
	want := runHelper(t, "user-agent", foundryHostingEnvVar+"=", userAgentTelemetryDisabledEnvVar+"=")
	if got := runHelper(t, "prepend-nil", userAgentTelemetryDisabledEnvVar+"="); got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
}

func TestPrependAgentFrameworkToUserAgentDisabled(t *testing.T) {
	got := runHelper(t, "prepend-disabled", userAgentTelemetryDisabledEnvVar+"=1")
	want := "my-app/1.0\ntrue\ntrue"
	if got != want {
		t.Fatalf("helper output = %q, want %q", got, want)
	}
}

func TestPrependAgentFrameworkToHTTPHeader(t *testing.T) {
	got := runHelper(t, "prepend-http", userAgentTelemetryDisabledEnvVar+"=")
	want := runHelper(t, "user-agent", foundryHostingEnvVar+"=", userAgentTelemetryDisabledEnvVar+"=") + " my-app/1.0"
	if got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
}

func TestPrependAgentFrameworkToHTTPHeaderIsIdempotent(t *testing.T) {
	got := runHelper(t, "prepend-http-twice", userAgentTelemetryDisabledEnvVar+"=")
	want := runHelper(t, "user-agent", foundryHostingEnvVar+"=", userAgentTelemetryDisabledEnvVar+"=") + " my-app/1.0"
	if got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
}

func TestPrependAgentFrameworkToHTTPHeaderHostedReplacesBareProduct(t *testing.T) {
	got := runHelper(t, "prepend-http-hosted-over-bare", foundryHostingEnvVar+"=1", userAgentTelemetryDisabledEnvVar+"=")
	want := runHelper(t, "user-agent", foundryHostingEnvVar+"=1", userAgentTelemetryDisabledEnvVar+"=") + " my-app/1.0"
	if got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
}

func TestPrependAgentFrameworkToUserAgentRecognizesMixedCaseProduct(t *testing.T) {
	for _, hosted := range []string{"", "1"} {
		t.Run(map[string]string{"": "bare", "1": "hosted"}[hosted], func(t *testing.T) {
			got := runHelper(t, "prepend-map-mixed-case", foundryHostingEnvVar+"="+hosted, userAgentTelemetryDisabledEnvVar+"=")
			want := strings.ToUpper(runHelper(t, "user-agent", foundryHostingEnvVar+"="+hosted, userAgentTelemetryDisabledEnvVar+"=")) + " my-app/1.0"
			if got != want {
				t.Fatalf("User-Agent = %q, want %q", got, want)
			}
		})
	}
}

func TestIsUserAgentTelemetryEnabledCached(t *testing.T) {
	got := runHelper(t, "telemetry-enabled-cached", userAgentTelemetryDisabledEnvVar+"=")
	if got != "true\ntrue" {
		t.Fatalf("helper output = %q, want true true", got)
	}
}

func TestUserAgentHostedEnvironmentPrefix(t *testing.T) {
	got := runHelper(t, "hosted-prefix", foundryHostingEnvVar+"=1", userAgentTelemetryDisabledEnvVar+"=")
	want := "foundry-hosting/" + runHelper(t, "user-agent", foundryHostingEnvVar+"=", userAgentTelemetryDisabledEnvVar+"=")
	if got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
}

func TestUserAgentHostedEnvironmentDetectionCached(t *testing.T) {
	got := runHelper(t, "hosted-cached", foundryHostingEnvVar+"=", userAgentTelemetryDisabledEnvVar+"=")
	base := runHelper(t, "user-agent", foundryHostingEnvVar+"=", userAgentTelemetryDisabledEnvVar+"=")
	want := base + "\n" + base
	if got != want {
		t.Fatalf("helper output = %q, want %q", got, want)
	}
}

func TestFeatureUsageBoundaryIndexes(t *testing.T) {
	for _, tc := range []struct {
		index int
		token string
	}{
		{0, "v1.1"},
		{63, "v1.8000000000000000"},
		{64, "v1.10000000000000000"},
		{127, "v1.80000000000000000000000000000000"},
	} {
		t.Run(strconv.Itoa(tc.index), func(t *testing.T) {
			got := runHelper(t, "feature-index:"+strconv.Itoa(tc.index))
			if want := "(feat=" + tc.token + ")"; got != want {
				t.Fatalf("feature comment = %q, want %q", got, want)
			}
		})
	}
}

func TestFeatureUsageInvalidIndexes(t *testing.T) {
	for _, index := range []int{-1, 128, -int(^uint(0)>>1) - 1, int(^uint(0) >> 1)} {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			got := runHelper(t, "feature-index:"+strconv.Itoa(index))
			want := fmt.Sprintf("panic: feature index %d must be in the range 0 through 127", index)
			if got != want {
				t.Fatalf("helper output = %q, want %q", got, want)
			}
		})
	}
}

func TestFeatureUsageConcurrentAccumulation(t *testing.T) {
	got := runHelper(t, "feature-concurrent")
	if want := "(feat=v1." + strings.Repeat("f", 32) + ")"; got != want {
		t.Fatalf("feature comment = %q, want %q", got, want)
	}
}

func TestFeatureUsageDuplicateAndChangedIndexes(t *testing.T) {
	got := runHelper(t, "feature-duplicates")
	want := "(feat=v1.1000)\n(feat=v1.1000)\n(feat=v1.3000)"
	if got != want {
		t.Fatalf("helper output = %q, want %q", got, want)
	}
}

func TestFeatureUsagePadsLowLane(t *testing.T) {
	got := runHelper(t, "feature-lanes")
	if want := "(feat=v1.20000000000000002)"; got != want {
		t.Fatalf("feature comment = %q, want %q", got, want)
	}
}

func TestFeatureUsageDisabled(t *testing.T) {
	for _, value := range []string{"true", "TRUE", "TrUe", "1"} {
		t.Run(value, func(t *testing.T) {
			if got := runHelper(t, "feature-disabled", featureMaskDisabledEnvVar+"="+value); got != "app/1.0" {
				t.Fatalf("User-Agent = %q, want app/1.0", got)
			}
			if got := runHelper(t, "feature-index:128", featureMaskDisabledEnvVar+"="+value); got != "" {
				t.Fatalf("disabled marker output = %q, want empty", got)
			}
		})
	}
}

func TestFeatureUsageDisabledFlagOtherValues(t *testing.T) {
	for _, value := range []string{"", "false", "0", "yes", " true "} {
		t.Run(value, func(t *testing.T) {
			got := runHelper(t, "feature-index:0", featureMaskDisabledEnvVar+"="+value)
			if got != "(feat=v1.1)" {
				t.Fatalf("feature comment = %q, want (feat=v1.1)", got)
			}
		})
	}
}

func TestFeatureUsageDisabledDoesNotValidateIndex(t *testing.T) {
	got := runHelper(t, "feature-index:128", featureMaskDisabledEnvVar+"=true")
	if got != "" {
		t.Fatalf("disabled marker output = %q, want no panic or token", got)
	}
}

func TestFeatureUsageConfigurationCached(t *testing.T) {
	for _, tc := range []struct {
		helper, want string
	}{
		{"feature-config-before-mark", "(feat=v1.80)"},
		{"feature-config-cached", "(feat=v1.180)"},
	} {
		t.Run(tc.helper, func(t *testing.T) {
			if got := runHelper(t, tc.helper); got != tc.want {
				t.Fatalf("feature comment = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestInMemoryHistoryFeatureUsageActivation(t *testing.T) {
	if got := runHelper(t, "feature-history"); got != "(feat=v1.2000)" {
		t.Fatalf("feature comment = %q, want history bit 13 only", got)
	}
}

func TestApplyToUserAgentNoTokenPreservesHeader(t *testing.T) {
	for _, input := range []string{
		"",
		"app/1.0",
		"  app/1.0  ",
		"app/1.0 (custom=value)",
		"app/1.0 (feat=v1.)",
		"app/1.0 (feat=vx.1)",
		"app/1.0 (feat=v1.1g)",
		"app/1.0(feat=v1.1)",
		"app/1.0 (feat=v1.1)suffix",
	} {
		t.Run(input, func(t *testing.T) {
			got := runHelperOutput(t, "feature-apply-empty", featureUserAgentEnvVar+"="+input)
			if got != input {
				t.Fatalf("User-Agent = %q, want unchanged %q", got, input)
			}
		})
	}
}

func TestApplyToUserAgentExcludedStripsFeatureComments(t *testing.T) {
	for _, tc := range []struct {
		input, want string
	}{
		{"app/1.0 (feat=v1.1)", "app/1.0"},
		{"(feat=v1.1)", ""},
		{"(feat=v1.1) app/1.0", "app/1.0"},
		{"app/1.0 (feat=v2.AB)", "app/1.0"},
		{"app/1.0 (feat=v1.1) (feat=v2.2)", "app/1.0"},
		{"app/1.0  (feat=v1.1)", "app/1.0 "},
		{"app/1.0 (custom=a (feat=v1.1) custom=b)", "app/1.0 (custom=a (feat=v1.1) custom=b)"},
		{`app/1.0 (custom=a \(feat=v1.1\) custom=b)`, `app/1.0 (custom=a \(feat=v1.1\) custom=b)`},
		{`app/1.0 (custom=a \) still=b) (feat=v1.1)`, `app/1.0 (custom=a \) still=b)`},
		{"app/1.0 (custom=a (feat=v1.1) custom=b) (feat=v2.2)", "app/1.0 (custom=a (feat=v1.1) custom=b)"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got := runHelperOutput(t, "feature-excluded", featureUserAgentEnvVar+"="+tc.input)
			if got != tc.want {
				t.Fatalf("User-Agent = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestApplyToUserAgentFeatureComments(t *testing.T) {
	for _, tc := range []struct {
		input, want string
	}{
		{"", ""},
		{"app/1.0", "app/1.0"},
		{"  app/1.0  ", "  app/1.0  "},
		{"app/1.0 (custom=value)", "app/1.0 (custom=value)"},
		{"app/1.0 (feat=v1.)", "app/1.0 (feat=v1.)"},
		{"app/1.0 (feat=vx.1)", "app/1.0 (feat=vx.1)"},
		{"app/1.0 (feat=v1.1g)", "app/1.0 (feat=v1.1g)"},
		{"app/1.0(feat=v1.1)", "app/1.0(feat=v1.1)"},
		{"app/1.0 (feat=v1.1)suffix", "app/1.0 (feat=v1.1)suffix"},
		{"app/1.0 (FEAT=v1.1)", "app/1.0 (FEAT=v1.1)"},
		{"app/1.0 (feat=v1.1)", "app/1.0"},
		{"(feat=v1.1)", ""},
		{"(feat=v1.1) app/1.0", "app/1.0"},
		{"app/1.0 (feat=v2.AB)", "app/1.0"},
		{"app/1.0 (feat=v1.1) (feat=v2.2)", "app/1.0"},
		{"app/1.0  (feat=v1.1)", "app/1.0 "},
		{"app/1.0 (custom=a (feat=v1.1) custom=b)", "app/1.0 (custom=a (feat=v1.1) custom=b)"},
		{`app/1.0 (custom=a \(feat=v1.1\) custom=b)`, `app/1.0 (custom=a \(feat=v1.1\) custom=b)`},
		{`app/1.0 (custom=a \) still=b) (feat=v1.1)`, `app/1.0 (custom=a \) still=b)`},
		{"app/1.0 (custom=a (feat=v1.1) custom=b) (feat=v2.2)", "app/1.0 (custom=a (feat=v1.1) custom=b)"},
		{"app/1.0\t(feat=v1.1)\t(custom=a)", "app/1.0\t(custom=a)"},
		{"(feat=v1.1)\u2003app/1.0", "app/1.0"},
		{"app/1.0\u2003(feat=v1.1)\u2003(custom=a)", "app/1.0\u2003(custom=a)"},
		{"(feat=v1.) (feat=v2.2) (custom=a)", "(feat=v1.) (custom=a)"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			for _, helper := range []string{"feature-apply-empty", "feature-strip"} {
				got := runHelperOutput(t, helper, featureUserAgentEnvVar+"="+tc.input)
				if got != tc.want {
					t.Fatalf("%s: User-Agent = %q, want %q", helper, got, tc.want)
				}
			}
		})
	}
}

func TestApplyToUserAgentRefreshesFeatureComments(t *testing.T) {
	got := runHelper(t, "feature-refresh")
	base := "vendor/2.0 (custom=a) app/1.0"
	want := base + " (feat=v1.20)\n" + base + " (feat=v1.20)\n" + base + " (feat=v1.20000000000000020)"
	if got != want {
		t.Fatalf("helper output = %q, want %q", got, want)
	}
}

func TestFeatureUsageRequestOrigin(t *testing.T) {
	base := runHelper(t, "user-agent") + " my-app/1.0"
	for _, tc := range []struct {
		url      string
		approved bool
	}{
		{"https://project.openai.azure.com/v1", true},
		{"https://openai.azure.com:8443/v1", true},
		{"https://PROJECT.OPENAI.AZURE.COM./v1", true},
		{"https://project.cognitiveservices.azure.com/v1", true},
		{"https://project.services.ai.azure.com/v1", true},
		{"https://project.inference.ai.azure.com/v1", false},
		{"https://api.openai.com/v1", false},
		{"https://openai.azure.com.example.test/v1", false},
		{"https://notopenai.azure.com/v1", false},
		{"https://openai.azure.com@example.test/v1", false},
		{"https://example.test/v1", false},
		{"http://project.openai.azure.com/v1", false},
		{"/relative", false},
		{"", false},
	} {
		t.Run(tc.url, func(t *testing.T) {
			got := runHelper(t, "feature-request", "AGENT_FRAMEWORK_TEST_URL="+tc.url)
			want := base
			if tc.approved {
				want += " (feat=v1.1)"
			}
			if got != want {
				t.Fatalf("User-Agent = %q, want %q", got, want)
			}
		})
	}
}

func TestFeatureUsageRequestOptOut(t *testing.T) {
	for _, tc := range []struct {
		name, env, want string
	}{
		{"mask", featureMaskDisabledEnvVar + "=true", runHelper(t, "user-agent") + " my-app/1.0"},
		{"user-agent", userAgentTelemetryDisabledEnvVar + "=true", "my-app/1.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runHelper(t, "feature-request", "AGENT_FRAMEWORK_TEST_URL=https://project.openai.azure.com/v1", tc.env)
			if got != tc.want {
				t.Fatalf("User-Agent = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnv) != "1" {
		return
	}
	_, helperName, ok := strings.Cut(strings.Join(os.Args, "\x00"), "\x00--\x00")
	if !ok {
		fmt.Fprint(os.Stderr, "missing helper name")
		os.Exit(2)
	}
	helperName = strings.Trim(helperName, "\x00")
	if indexText, ok := strings.CutPrefix(helperName, "feature-index:"); ok {
		index, err := strconv.Atoi(indexText)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer func() {
				if value := recover(); value != nil {
					fmt.Printf("panic: %v", value)
				}
			}()
			telemetry.MarkUsed(index)
			fmt.Print(telemetry.ApplyToUserAgent("", true))
		}()
		os.Exit(0)
	}
	if name, ok := strings.CutPrefix(helperName, "activation:"); ok {
		runFeatureActivation(t, name)
		if t.Failed() {
			t.FailNow()
		}
		os.Exit(0)
	}
	switch helperName {
	case "user-agent":
		headers := telemetry.PrependAgentFrameworkToUserAgent(nil)
		fmt.Print(headers[userAgentKey])
	case "prepend-map":
		headers := telemetry.PrependAgentFrameworkToUserAgent(map[string]string{"User-Agent": "my-app/1.0"})
		fmt.Print(headers[userAgentKey])
	case "prepend-map-twice":
		headers := telemetry.PrependAgentFrameworkToUserAgent(map[string]string{"User-Agent": "my-app/1.0"})
		headers = telemetry.PrependAgentFrameworkToUserAgent(headers)
		fmt.Print(headers[userAgentKey])
	case "prepend-nil":
		headers := telemetry.PrependAgentFrameworkToUserAgent(nil)
		fmt.Print(headers[userAgentKey])
	case "prepend-disabled":
		headers := telemetry.PrependAgentFrameworkToUserAgent(map[string]string{"User-Agent": "my-app/1.0"})
		fmt.Println(headers[userAgentKey])
		fmt.Println(telemetry.PrependAgentFrameworkToUserAgent(nil) == nil)
		fmt.Print(telemetry.PrependAgentFrameworkToHTTPHeader(nil) == nil)
	case "prepend-http":
		headers := telemetry.PrependAgentFrameworkToHTTPHeader(http.Header{"User-Agent": []string{"my-app/1.0"}})
		fmt.Print(headers.Get(userAgentKey))
	case "prepend-http-twice":
		headers := telemetry.PrependAgentFrameworkToHTTPHeader(http.Header{"User-Agent": []string{"my-app/1.0"}})
		headers = telemetry.PrependAgentFrameworkToHTTPHeader(headers)
		fmt.Print(headers.Get(userAgentKey))
	case "prepend-http-hosted-over-bare":
		headers := telemetry.PrependAgentFrameworkToHTTPHeader(nil)
		bareUserAgent := strings.TrimPrefix(headers.Get(userAgentKey), "foundry-hosting/")
		headers = telemetry.PrependAgentFrameworkToHTTPHeader(http.Header{"User-Agent": []string{bareUserAgent + " my-app/1.0"}})
		fmt.Print(headers.Get(userAgentKey))
	case "prepend-map-mixed-case":
		headers := telemetry.PrependAgentFrameworkToUserAgent(nil)
		mixedCase := strings.ToUpper(headers[userAgentKey])
		headers = telemetry.PrependAgentFrameworkToUserAgent(map[string]string{"User-Agent": mixedCase + " my-app/1.0"})
		fmt.Print(headers[userAgentKey])
	case "telemetry-enabled-cached":
		fmt.Println(telemetry.PrependAgentFrameworkToUserAgent(nil) != nil)
		_ = os.Setenv(userAgentTelemetryDisabledEnvVar, "true")
		fmt.Print(telemetry.PrependAgentFrameworkToUserAgent(nil) != nil)
	case "hosted-prefix":
		headers := telemetry.PrependAgentFrameworkToUserAgent(nil)
		fmt.Print(headers[userAgentKey])
	case "hosted-cached":
		headers := telemetry.PrependAgentFrameworkToUserAgent(nil)
		fmt.Println(headers[userAgentKey])
		_ = os.Setenv(foundryHostingEnvVar, "1")
		headers = telemetry.PrependAgentFrameworkToUserAgent(nil)
		fmt.Print(headers[userAgentKey])
	case "feature-concurrent":
		var wg sync.WaitGroup
		start := make(chan struct{})
		for index := range 128 {
			wg.Go(func() {
				<-start
				telemetry.MarkUsed(index)
				_ = telemetry.ApplyToUserAgent("app/1.0", true)
			})
		}
		close(start)
		wg.Wait()
		fmt.Print(telemetry.ApplyToUserAgent("", true))
	case "feature-duplicates":
		telemetry.MarkUsed(12)
		fmt.Println(telemetry.ApplyToUserAgent("", true))
		var wg sync.WaitGroup
		for range 100 {
			wg.Go(func() { telemetry.MarkUsed(12) })
		}
		wg.Wait()
		fmt.Println(telemetry.ApplyToUserAgent("", true))
		telemetry.MarkUsed(13)
		fmt.Print(telemetry.ApplyToUserAgent("", true))
	case "feature-lanes":
		telemetry.MarkUsed(1)
		telemetry.MarkUsed(65)
		fmt.Print(telemetry.ApplyToUserAgent("", true))
	case "feature-disabled":
		telemetry.MarkUsed(0)
		telemetry.MarkUsed(127)
		if err := os.Setenv(featureMaskDisabledEnvVar, ""); err != nil {
			t.Fatal(err)
		}
		telemetry.MarkUsed(0)
		fmt.Print(telemetry.ApplyToUserAgent("app/1.0 (feat=v1.1)", true))
	case "feature-config-cached":
		telemetry.MarkUsed(7)
		if err := os.Setenv(featureMaskDisabledEnvVar, "true"); err != nil {
			t.Fatal(err)
		}
		telemetry.MarkUsed(8)
		fmt.Print(telemetry.ApplyToUserAgent("", true))
	case "feature-config-before-mark":
		if got := telemetry.ApplyToUserAgent("", true); got != "" {
			t.Fatalf("initial feature comment = %q, want empty", got)
		}
		if err := os.Setenv(featureMaskDisabledEnvVar, "true"); err != nil {
			t.Fatal(err)
		}
		telemetry.MarkUsed(7)
		fmt.Print(telemetry.ApplyToUserAgent("", true))
	case "feature-history":
		provider := agent.NewInMemoryHistoryProvider(agent.InMemoryHistoryProviderConfig{})
		invoking := agent.InvokingContext{Options: []agent.Option{agent.WithSession(new(agent.Session))}}
		if got := telemetry.ApplyToUserAgent("", true); got != "" {
			t.Fatalf("construction marked features: %s", got)
		}
		if _, err := provider.Invoking(t.Context(), invoking); err != nil {
			t.Fatal(err)
		}
		fmt.Print(telemetry.ApplyToUserAgent("", true))
	case "feature-apply-empty", "feature-strip", "feature-excluded":
		if helperName == "feature-strip" {
			telemetry.MarkUsed(5)
		}
		fmt.Print(telemetry.ApplyToUserAgent(os.Getenv(featureUserAgentEnvVar), helperName == "feature-apply-empty"))
	case "feature-refresh":
		telemetry.MarkUsed(5)
		refreshed := telemetry.ApplyToUserAgent("vendor/2.0 (custom=a) app/1.0 (feat=v1.1)", true)
		fmt.Println(refreshed)
		fmt.Println(telemetry.ApplyToUserAgent(refreshed, true))
		telemetry.MarkUsed(65)
		fmt.Print(telemetry.ApplyToUserAgent(refreshed, true))
	case "feature-request":
		telemetry.MarkUsed(0)
		var destination *url.URL
		if value := os.Getenv("AGENT_FRAMEWORK_TEST_URL"); value != "" {
			var err error
			destination, err = url.Parse(value)
			if err != nil {
				t.Fatal(err)
			}
		}
		req := &http.Request{
			URL: destination,
			Header: http.Header{
				userAgentKey: []string{"my-app/1.0 (feat=v2.AB)"},
				"X-Test":     []string{"preserved"},
			},
		}
		telemetry.ApplyToHTTPRequest(req)
		if req.Header.Get("X-Test") != "preserved" {
			t.Fatal("unrelated request header changed")
		}
		fmt.Print(req.Header.Get(userAgentKey))
	default:
		fmt.Fprintf(os.Stderr, "unknown helper %q", helperName)
		os.Exit(2)
	}
	os.Exit(0)
}

func runHelper(t *testing.T, name string, env ...string) string {
	t.Helper()
	return strings.TrimSpace(runHelperOutput(t, name, env...))
}

func runHelperOutput(t *testing.T, name string, env ...string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() failed: %v", err)
	}
	cmd := exec.Command(exe, "-test.run=^TestHelperProcess$", "--", name)
	cmd.Env = append(os.Environ(), foundryHostingEnvVar+"=", userAgentTelemetryDisabledEnvVar+"=", featureMaskDisabledEnvVar+"=")
	cmd.Env = append(cmd.Env, env...)
	cmd.Env = append(cmd.Env, helperProcessEnv+"=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("helper %q failed: %v\n%s%s", name, err, out, stderr.String())
	}
	return string(out)
}
