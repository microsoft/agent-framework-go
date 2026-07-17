// Copyright (c) Microsoft. All rights reserved.

package anthropicprovider

// The behavioral wire-format cache_control tests are black-box and live in
// agent_test.go. What remains here is the one invariant that is inherently
// white-box: it drives itself off the Anthropic SDK's own ContentBlockParamUnion
// and calls the unexported reflective setCacheControl to prove EVERY SDK variant
// is handled. The public agent path can only produce a handful of the SDK's block
// variants, so this exhaustive guard cannot be expressed black-box without losing
// the coverage that is its entire point (catching a future SDK variant, or a
// hand-written switch that silently skips the twelve non-obvious cacheable
// variants). It therefore stays package-internal.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

// Marking a block that has no cache_control field on the wire is a clean no-op.
//
// thinking and redacted_thinking are the only two content variants with no
// cache_control field, so a breakpoint cannot sit on them. setCacheControl must
// report false and leave the block untouched rather than panic -- degraded (that
// block uncached) beats a crash.
func TestSetCacheControl_NoFieldBlockIsCleanNoop(t *testing.T) {
	thinking := anthropic.NewThinkingBlock("sig", "let me think")
	if got := setCacheControl(&thinking, ""); got != false {
		t.Fatalf("setCacheControl on a thinking block = %v, want false", got)
	}
	// The block must be unchanged and still marshal cleanly (no panic, no cache_control).
	raw, err := json.Marshal(thinking)
	if err != nil {
		t.Fatalf("marshal thinking block: %v", err)
	}
	if strings.Contains(string(raw), "cache_control") {
		t.Fatalf("thinking block gained a cache_control it cannot carry:\n%s", raw)
	}
}

// The invariant, guarded against a moving SDK.
//
// Every ContentBlockParamUnion variant whose param struct HAS a cache_control field
// must actually receive a breakpoint, and every variant that lacks one must report
// false. This drives itself off the union's own reflected shape, so when the SDK
// adds an 18th variant this test covers it the day it lands -- and if anyone ever
// replaces the reflective setCacheControl with a hand-written switch, it fails for
// whatever they forgot. That is the point: a switch covering the obvious five would
// silently skip twelve cacheable variants, including every server-tool result.
//
// It also proves the invariant this whole design rests on: for each variant exactly
// one pointer field of the union is non-nil, which is what lets setCacheControl
// treat "the first non-nil field" as the block's real type.
func TestSetCacheControl_CoversEveryCacheableVariant(t *testing.T) {
	union := reflect.TypeOf(anthropic.ContentBlockParamUnion{})

	var cacheable, skipped int
	for i := range union.NumField() {
		field := union.Field(i)
		if field.Type.Kind() != reflect.Pointer {
			continue // the embedded paramUnion marker
		}

		// Build a union with just this variant populated.
		block := anthropic.ContentBlockParamUnion{}
		variant := reflect.New(field.Type.Elem())
		reflect.ValueOf(&block).Elem().Field(i).Set(variant)

		// Exactly one non-nil pointer = the variant. Guard that invariant directly.
		if n := nonNilPointerFields(block); n != 1 {
			t.Fatalf("%s: union has %d non-nil pointer fields, want exactly 1", field.Name, n)
		}

		_, wantsCache := field.Type.Elem().FieldByName("CacheControl")
		got := setCacheControl(&block, Ephemeral1h)

		if got != wantsCache {
			t.Errorf("%s: setCacheControl = %v, but the wire type %s a cache_control field",
				field.Name, got, map[bool]string{true: "HAS", false: "has no"}[wantsCache])
			continue
		}
		if wantsCache {
			cacheable++
			cc := variant.Elem().FieldByName("CacheControl")
			ephemeral := cc.Interface().(anthropic.CacheControlEphemeralParam)
			if ephemeral.Type != "ephemeral" {
				t.Errorf("%s: reported success but wrote no ephemeral breakpoint", field.Name)
			}
			// TTL must flow through the reflective setter, not get dropped.
			if ephemeral.TTL != Ephemeral1h {
				t.Errorf("%s: ttl = %q, want %q", field.Name, ephemeral.TTL, Ephemeral1h)
			}
		} else {
			skipped++
		}
	}

	// Guard the guard: if the union were ever read as empty, every assertion above
	// would vacuously pass and this test would prove nothing.
	if cacheable < 10 || skipped != 2 {
		t.Fatalf("union shape looks wrong: %d cacheable, %d non-cacheable "+
			"(expected 10+ cacheable and exactly 2 non-cacheable: thinking, redacted_thinking)",
			cacheable, skipped)
	}
}

// nonNilPointerFields counts the non-nil pointer fields of a ContentBlockParamUnion,
// the "exactly one variant is populated" invariant setCacheControl relies on.
func nonNilPointerFields(block anthropic.ContentBlockParamUnion) int {
	v := reflect.ValueOf(block)
	var n int
	for i := range v.NumField() {
		f := v.Field(i)
		if f.Kind() == reflect.Pointer && !f.IsNil() {
			n++
		}
	}
	return n
}
