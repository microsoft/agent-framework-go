// Copyright (c) Microsoft. All rights reserved.

package telemetry_test

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	maftelemetry "github.com/microsoft/agent-framework-go/telemetry"
)

const featureUsageHelperEnv = "AGENT_FRAMEWORK_FEATURE_USAGE_TEST_HELPER"

func TestFeatureUsageMarkUsedAndApplyToUserAgent(t *testing.T) {
	got := runFeatureUsageHelper(t, "mark-openai")
	if got != "app/1.0 (feat=v1.40000000000000)" {
		t.Fatalf("ApplyToUserAgent = %q, want app/1.0 (feat=v1.40000000000000)", got)
	}
}

func TestFeatureUsageApplyToUserAgentRemovesStaleComments(t *testing.T) {
	got := runFeatureUsageHelper(t, "strip-stale")
	if got != "vendor/2.0 app/1.0" {
		t.Fatalf("ApplyToUserAgent = %q, want vendor/2.0 app/1.0", got)
	}
}

func TestFeatureUsageMarkUsedRejectsInvalidIndex(t *testing.T) {
	got := runFeatureUsageHelper(t, "invalid-index")
	if got != "panic" {
		t.Fatalf("helper output = %q, want panic", got)
	}
}

func TestFeatureUsageDisabledSkipsMarksAndStripsTokens(t *testing.T) {
	got := runFeatureUsageHelper(t, "disabled", "AGENT_FRAMEWORK_FEATURE_MASK_DISABLED=1")
	if got != "app/1.0" {
		t.Fatalf("ApplyToUserAgent = %q, want app/1.0", got)
	}
}

func TestFeatureUsageHelperProcess(t *testing.T) {
	if os.Getenv(featureUsageHelperEnv) != "1" {
		return
	}

	_, helperName, ok := strings.Cut(strings.Join(os.Args, "\x00"), "\x00--\x00")
	if !ok {
		fmt.Fprint(os.Stderr, "missing helper name")
		os.Exit(2)
	}

	switch strings.Trim(helperName, "\x00") {
	case "mark-openai":
		maftelemetry.FeatureUsage.MarkUsed(54)
		fmt.Print(maftelemetry.FeatureUsage.ApplyToUserAgent("app/1.0"))
	case "strip-stale":
		fmt.Print(maftelemetry.FeatureUsage.ApplyToUserAgent("vendor/2.0 (feat=v1.ff) app/1.0 (feat=v1.1)", false))
	case "invalid-index":
		defer func() {
			if recover() != nil {
				fmt.Print("panic")
				os.Exit(0)
			}
			fmt.Print("no-panic")
			os.Exit(0)
		}()
		maftelemetry.FeatureUsage.MarkUsed(128)
	case "disabled":
		maftelemetry.FeatureUsage.MarkUsed(54)
		fmt.Print(maftelemetry.FeatureUsage.ApplyToUserAgent("app/1.0 (feat=v1.ff)"))
	default:
		fmt.Fprintf(os.Stderr, "unknown helper %q", helperName)
		os.Exit(2)
	}

	os.Exit(0)
}

func runFeatureUsageHelper(t *testing.T, name string, env ...string) string {
	t.Helper()

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() failed: %v", err)
	}
	cmd := exec.Command(exe, "-test.run=^TestFeatureUsageHelperProcess$", "--", name)
	cmd.Env = append(os.Environ(), env...)
	cmd.Env = append(cmd.Env, featureUsageHelperEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helper %q failed: %v\n%s", name, err, out)
	}
	return strings.TrimSpace(string(out))
}
