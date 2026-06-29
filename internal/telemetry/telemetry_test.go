// Copyright (c) Microsoft. All rights reserved.

package telemetry_test

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/microsoft/agent-framework-go/internal/telemetry"
)

const helperProcessEnv = "AGENT_FRAMEWORK_TELEMETRY_TEST_HELPER"

const (
	foundryHostingEnvVar             = "FOUNDRY_HOSTING_ENVIRONMENT"
	userAgentKey                     = "User-Agent"
	userAgentTelemetryDisabledEnvVar = "AGENT_FRAMEWORK_USER_AGENT_DISABLED"
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

func TestHelperProcess(t *testing.T) {
	if os.Getenv(helperProcessEnv) != "1" {
		return
	}
	_, helperName, ok := strings.Cut(strings.Join(os.Args, "\x00"), "\x00--\x00")
	if !ok {
		fmt.Fprint(os.Stderr, "missing helper name")
		os.Exit(2)
	}
	switch strings.Trim(helperName, "\x00") {
	case "user-agent":
		headers := telemetry.PrependAgentFrameworkToUserAgent(nil)
		fmt.Print(headers[userAgentKey])
	case "prepend-map":
		headers := telemetry.PrependAgentFrameworkToUserAgent(map[string]string{"User-Agent": "my-app/1.0"})
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
	default:
		fmt.Fprintf(os.Stderr, "unknown helper %q", helperName)
		os.Exit(2)
	}
	os.Exit(0)
}

func runHelper(t *testing.T, name string, env ...string) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() failed: %v", err)
	}
	cmd := exec.Command(exe, "-test.run=^TestHelperProcess$", "--", name)
	cmd.Env = append(os.Environ(), env...)
	cmd.Env = append(cmd.Env, helperProcessEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper %q failed: %v\n%s", name, err, out)
	}
	return strings.TrimSpace(string(out))
}
