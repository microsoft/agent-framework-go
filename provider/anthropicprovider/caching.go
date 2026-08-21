// Copyright (c) Microsoft. All rights reserved.

package anthropicprovider

import (
	"reflect"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/microsoft/agent-framework-go/message"
)

// Ephemeral5m and Ephemeral1h are the cache lifetimes Anthropic supports for a
// cache_control breakpoint. They are the anthropic-sdk-go values, surfaced here as
// named constants so callers do not have to reach for the SDK's verbose identifiers.
//
// Their type is the SDK's own anthropic.CacheControlEphemeralTTL, deliberately: this
// package already hands raw SDK types across its public surface (see MessageNewParams),
// so a provider-specific caching primitive stays consistent by doing the same rather
// than wrapping the SDK enum in a parallel type the caller would have to convert.
const (
	// Ephemeral5m caches for 5 minutes. This is also the default when no TTL is set.
	Ephemeral5m = anthropic.CacheControlEphemeralTTLTTL5m
	// Ephemeral1h caches for 1 hour.
	Ephemeral1h = anthropic.CacheControlEphemeralTTLTTL1h
)

// CacheControl describes an Anthropic cache_control breakpoint on a single content block.
type CacheControl struct {
	// TTL is the cache lifetime. The zero value ("") means the Anthropic default of
	// 5-minute ephemeral caching; Ephemeral1h selects the 1-hour tier.
	TTL anthropic.CacheControlEphemeralTTL
}

// CacheControlOption configures the CacheControl attached by WithCacheControl.
type CacheControlOption func(*CacheControl)

// WithTTL sets the cache lifetime for the breakpoint. Passing the zero value leaves
// Anthropic's default (5 minutes) in effect.
func WithTTL(ttl anthropic.CacheControlEphemeralTTL) CacheControlOption {
	return func(cc *CacheControl) { cc.TTL = ttl }
}

// cacheControlKey is the package-private key under which a cache_control marker is stored
// in a content's AdditionalProperties bag. Unexported and package-qualified so it cannot
// collide with a key a caller sets for their own purposes.
const cacheControlKey = "anthropicprovider.cacheControl"

// WithCacheControl attaches an Anthropic cache_control breakpoint to a single
// message.Content, in place, and returns it for chaining. Caching is expressed on the
// individual content block, not toggled agent-wide, so the caller decides exactly which
// blocks become cache breakpoints.
//
// The marker is stored in the content's ContentHeader.AdditionalProperties under a
// package-private key and read back in buildMessageParam, which transfers it onto whatever
// Anthropic block the content becomes: text, image, document, tool-use, or tool-result. A
// content whose block type has no cache_control field on the wire (the thinking and
// redacted_thinking variants) is a clean no-op rather than an error.
//
// Opt-in, deliberately. A cache WRITE costs more than a plain input token, so caching is a
// net loss for a short single-turn prompt; nothing here turns it on unless the caller asks.
//
// Breakpoint budget: Anthropic permits at most 4 cache_control breakpoints per request
// (the tool definitions, the system prompt, and message blocks all draw from the same
// budget). The caller is responsible for staying within it; this function does not dedupe
// or cap, and a request with too many breakpoints is rejected by the API. Higher-level
// automatic placement is intentionally left to a future change.
//
// For full control over the raw request, including breakpoints on tools or the system
// prompt, use MessageNewParams as the low-level escape hatch.
func WithCacheControl(c message.Content, opts ...CacheControlOption) message.Content {
	var cc CacheControl
	for _, opt := range opts {
		opt(&cc)
	}
	markCacheControl(c, cc)
	return c
}

// markCacheControl writes the cache_control marker into a content's AdditionalProperties.
//
// Every message.Content implementation embeds message.ContentHeader by value, so each one
// already carries an AdditionalProperties bag for provider metadata. ContentHeader.Header()
// only hands back a COPY, though, so the field has to be reached through the content's own
// pointer to mutate the original. Reflection does that generically: it finds the promoted
// AdditionalProperties field on any content type, present and future, without a per-type
// switch that would silently miss a type the day it is added.
//
// TODO: this reflection dance can be dropped once message.Content exposes its ContentHeader
// by pointer (Header() returns a copy today). With a pointer accessor, markCacheControl can
// set AdditionalProperties on the original header directly, with no reflection.
func markCacheControl(c message.Content, cc CacheControl) {
	v := reflect.ValueOf(c)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return
	}
	f := v.Elem().FieldByName("AdditionalProperties")
	if !f.IsValid() || !f.CanSet() || f.Kind() != reflect.Map {
		return
	}
	if f.IsNil() {
		f.Set(reflect.MakeMap(f.Type()))
	}
	f.SetMapIndex(reflect.ValueOf(cacheControlKey), reflect.ValueOf(cc).Convert(f.Type().Elem()))
}

// cacheControlOf reads back a marker set by WithCacheControl. Reading uses the public
// Header() copy -- no reflection needed, because reading a value does not require mutating
// the original.
func cacheControlOf(c message.Content) (CacheControl, bool) {
	props := c.Header().AdditionalProperties
	if props == nil {
		return CacheControl{}, false
	}
	cc, ok := props[cacheControlKey].(CacheControl)
	return cc, ok
}

// applyContentCacheControl transfers a WithCacheControl marker from a message.Content onto
// the Anthropic block that was built from it. No marker means no change (opt-in). If the
// block type cannot carry a breakpoint, setCacheControl reports false and this is a no-op
// rather than a panic: degraded (that block simply uncached) beats a crash.
func applyContentCacheControl(c message.Content, block *anthropic.ContentBlockParamUnion) {
	cc, ok := cacheControlOf(c)
	if !ok {
		return
	}
	setCacheControl(block, cc.TTL)
}

var cacheControlType = reflect.TypeOf(anthropic.NewCacheControlEphemeralParam())

// setCacheControl marks a content block as a cache breakpoint with the given TTL,
// reporting whether it could. False means the populated variant has no cache_control field
// (thinking and redacted_thinking are the only two), so the block cannot carry one.
//
// This reflects over the union rather than switching on its variants, and that is a
// deliberate trade. ContentBlockParamUnion has 17 variants today; a hand-written switch
// covering the obvious five leaves the other twelve -- every server-tool result, which is
// to say the BIGGEST blocks in a modern agent's context -- silently uncached, and rots the
// moment the SDK adds an eighteenth. Reflection makes the SDK's own type definitions the
// source of truth: a block is cacheable exactly when its param struct carries the field.
// New variants are then handled the day they appear, correctly, with no edit here.
//
// The cost is one reflect walk over a 17-field struct per request. Next to a network call
// to an LLM, that is not a cost.
//
// An empty TTL leaves CacheControlEphemeralParam.TTL at its zero value, which the SDK
// omits from the wire, so Anthropic applies its 5-minute default.
func setCacheControl(b *anthropic.ContentBlockParamUnion, ttl anthropic.CacheControlEphemeralTTL) bool {
	v := reflect.ValueOf(b).Elem()
	for i := range v.NumField() {
		f := v.Field(i)
		if f.Kind() != reflect.Pointer || f.IsNil() {
			continue
		}
		// Exactly one variant is non-nil, so this is the block's real type.
		cc := f.Elem().FieldByName("CacheControl")
		if !cc.IsValid() || !cc.CanSet() || cc.Type() != cacheControlType {
			return false // e.g. thinking: no cache_control field exists on the wire type.
		}
		param := anthropic.NewCacheControlEphemeralParam()
		param.TTL = ttl
		cc.Set(reflect.ValueOf(param))
		return true
	}
	return false // empty union: nothing populated.
}
