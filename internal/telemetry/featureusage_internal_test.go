// Copyright (c) Microsoft. All rights reserved.

package telemetry

import (
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"sync"
	"testing"
)

func TestFoundryFeatureUsageApprovedOrigins(t *testing.T) {
	for _, value := range []string{
		"https://services.ai.azure.com/",
		"https://project.services.ai.azure.com/api/projects/test",
		"https://SERVICES.AI.AZURE.COM./",
		"https://inference.ai.azure.com/",
		"https://model.inference.ai.azure.com/models",
	} {
		t.Run(value, func(t *testing.T) {
			destination, err := url.Parse(value)
			if err != nil {
				t.Fatal(err)
			}
			if !approvedFeatureOrigin(destination, foundryHostSuffixes[:]) {
				t.Fatalf("Foundry origin %q was rejected", value)
			}
		})
	}
}

func TestFoundryFeatureUsageRejectedOrigins(t *testing.T) {
	for _, value := range []string{
		"http://project.services.ai.azure.com/",
		"https://services.ai.azure.com.example.com/",
		"https://projectservices.ai.azure.com/",
		"https://evilservices.ai.azure.com/",
		"https://inference.ai.azure.com.evil.test/",
		"https://openai.azure.com/",
		"https://example.com/",
	} {
		t.Run(value, func(t *testing.T) {
			destination, err := url.Parse(value)
			if err != nil {
				t.Fatal(err)
			}
			if approvedFeatureOrigin(destination, foundryHostSuffixes[:]) {
				t.Fatalf("unapproved Foundry origin %q was accepted", value)
			}
		})
	}
}

func TestFoundryFeatureUsagePolicyLiveToken(t *testing.T) {
	if runInternalFeatureUsageSubprocess(t) {
		return
	}
	var captured []string
	send := func(destination string) {
		req, err := http.NewRequest(http.MethodGet, destination, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set(userAgentKey, "app/1.0 (feat=v1.ff)")
		applyFeatureUsageToHTTPRequest(req, foundryHostSuffixes[:])
		captured = append(captured, req.Header.Get(userAgentKey))
	}

	MarkUsed(0)
	send("https://project.services.ai.azure.com/first")
	MarkUsed(2)
	send("https://project.services.ai.azure.com/second")

	if want := []string{"app/1.0 (feat=v1.1)", "app/1.0 (feat=v1.5)"}; !slices.Equal(captured, want) {
		t.Fatalf("captured User-Agents = %q, want %q", captured, want)
	}
}

func TestFoundryFeatureUsagePolicyEmptyTokenPreservesHeader(t *testing.T) {
	if runInternalFeatureUsageSubprocess(t) {
		return
	}
	const baseHeader = "vendor/2.0  app/1.0 (custom=value)"
	req, err := http.NewRequest(http.MethodGet, "https://project.services.ai.azure.com/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(userAgentKey, baseHeader)

	applyFeatureUsageToHTTPRequest(req, foundryHostSuffixes[:])

	if got := req.Header.Get(userAgentKey); got != baseHeader {
		t.Fatalf("User-Agent = %q, want byte-for-byte %q", got, baseHeader)
	}
}

func TestFoundryFeatureUsagePolicyIneligibleRemovesComment(t *testing.T) {
	if runInternalFeatureUsageSubprocess(t) {
		return
	}
	req, err := http.NewRequest(http.MethodGet, "https://project.services.ai.azure.com.evil.test/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(userAgentKey, "vendor/2.0  app/1.0 (feat=v1.1)")

	applyFeatureUsageToHTTPRequest(req, foundryHostSuffixes[:])

	if got := req.Header.Get(userAgentKey); got != "vendor/2.0  app/1.0" {
		t.Fatalf("User-Agent = %q, want unchanged base without stale comment", got)
	}
}

func TestFeatureUsageEmptyMaskReturnsNoToken(t *testing.T) {
	if runInternalFeatureUsageSubprocess(t) {
		return
	}
	if token := currentFeatureToken(); token != "" {
		t.Fatalf("empty mask token = %q, want empty", token)
	}
}

func TestFeatureUsageLowercaseVersionedHex(t *testing.T) {
	if runInternalFeatureUsageSubprocess(t) {
		return
	}
	for _, index := range []int{1, 63, 64, 127} {
		MarkUsed(index)
	}

	token := currentFeatureToken()

	if !regexp.MustCompile(`^v1\.[0-9a-f]{1,32}$`).MatchString(token) {
		t.Fatalf("token = %q, want lowercase versioned hexadecimal", token)
	}
}

func TestFeatureUsageTokenCache(t *testing.T) {
	if runInternalFeatureUsageSubprocess(t) {
		return
	}
	MarkUsed(12)
	original := currentFeatureToken()
	originalCache := cachedToken.Load()
	if originalCache == nil {
		t.Fatal("initial token was not cached")
	}

	cached := currentFeatureToken()
	if cachedToken.Load() != originalCache {
		t.Fatal("unchanged mask replaced the cached token")
	}
	MarkUsed(12)
	deduplicated := currentFeatureToken()
	if cachedToken.Load() != originalCache {
		t.Fatal("duplicate mark replaced the cached token")
	}
	MarkUsed(13)
	changed := currentFeatureToken()
	if cachedToken.Load() == originalCache {
		t.Fatal("changed mask retained the stale cached token")
	}

	if original != "v1.1000" || cached != original || deduplicated != original {
		t.Fatalf("unchanged tokens = %q, %q, %q, want v1.1000", original, cached, deduplicated)
	}
	if changed != "v1.3000" {
		t.Fatalf("changed token = %q, want v1.3000", changed)
	}
}

func TestFeatureUsageDuplicateIndexKeepsTokenCache(t *testing.T) {
	if runInternalFeatureUsageSubprocess(t) {
		return
	}
	MarkUsed(42)
	original := currentFeatureToken()
	originalCache := cachedToken.Load()
	if originalCache == nil {
		t.Fatal("initial token was not cached")
	}

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() { MarkUsed(42) })
	}
	wg.Wait()
	duplicate := currentFeatureToken()

	if duplicate != "v1.40000000000" || duplicate != original {
		t.Fatalf("duplicate token = %q, original = %q, want v1.40000000000", duplicate, original)
	}
	if cachedToken.Load() != originalCache {
		t.Fatal("duplicate marks replaced the cached token")
	}
}

func TestFeatureUsageDisabledDoesNotAccumulate(t *testing.T) {
	for _, value := range []string{"true", "TRUE", "TrUe", "1"} {
		t.Run(value, func(t *testing.T) {
			if runInternalFeatureUsageSubprocess(t, featureMaskDisabledEnvVar+"="+value) {
				return
			}

			MarkUsed(0)
			MarkUsed(127)
			if err := os.Setenv(featureMaskDisabledEnvVar, ""); err != nil {
				t.Fatal(err)
			}

			// The opt-out is cached, so inspect the accumulator rather than
			// mistaking a suppressed token for evidence that no bits were set.
			if low, high := featureLow.Load(), featureHigh.Load(); low != 0 || high != 0 {
				t.Fatalf("disabled accumulator = %x:%016x, want zero", high, low)
			}
			if token := currentFeatureToken(); token != "" {
				t.Fatalf("disabled token = %q, want empty", token)
			}
		})
	}
}

func runInternalFeatureUsageSubprocess(t *testing.T, env ...string) bool {
	t.Helper()
	const helperEnv = "AGENT_FRAMEWORK_FEATURE_USAGE_INTERNAL_TEST"
	if os.Getenv(helperEnv) == t.Name() {
		return false
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
	cmd.Env = append(os.Environ(), helperEnv+"="+t.Name(), featureMaskDisabledEnvVar+"=", userAgentTelemetryDisabledEnvVar+"=")
	cmd.Env = append(cmd.Env, env...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated feature-usage test failed: %v\n%s", err, output)
	}
	return true
}
