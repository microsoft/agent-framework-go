// Copyright (c) Microsoft. All rights reserved.

package azaiprojects

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// validateToolboxName guards every toolbox request builder that interpolates a
// caller-supplied name into the request path (for example
// "/toolboxes/{name}/versions"). It ensures the name resolves to a single,
// intact path segment so a caller-influenced value cannot change the request
// target — the Go counterpart of the upstream .NET FoundryToolboxService
// EnsureSafeToolboxName check (microsoft/agent-framework#6890).
//
// Unlike .NET, the Go request builders percent-escape the name with
// url.PathEscape before it reaches the wire, so an "effect-based" check that
// rebuilds and re-parses the URL would accept "a/b" (it round-trips as the
// escaped single segment "a%2Fb"). Instead this validates the name directly,
// rejecting path separators, traversal, and query/fragment delimiters in both
// the raw name and its percent-decoded form — an encoded separator such as
// "%2F" must be rejected here because a downstream proxy could decode it back
// into a separator.
func validateToolboxName(name string) error {
	if name == "" {
		return errors.New("parameter name cannot be empty")
	}
	if !isSafeToolboxSegment(name) {
		return fmt.Errorf("toolbox name %q is not a valid single-segment identifier: "+
			"it must resolve to a single path segment and must not alter the request target", name)
	}
	// A percent-encoded separator (e.g. "%2F") stays a single segment through
	// url.PathEscape but decodes back into a separator at a downstream proxy,
	// so validate the decoded form too. A malformed escape sequence cannot
	// round-trip to an intact segment, so treat a decode error as unsafe.
	decoded, err := url.PathUnescape(name)
	if err != nil || !isSafeToolboxSegment(decoded) {
		return fmt.Errorf("toolbox name %q is not a valid single-segment identifier: "+
			"it must resolve to a single path segment and must not alter the request target", name)
	}
	return nil
}

// isSafeToolboxSegment reports whether s is a single, intact URL path segment:
// non-empty, not a "."/".." traversal segment, and free of the separators and
// delimiters ("/", "\", "?", "#") that would move the request target.
func isSafeToolboxSegment(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	// "\\" is a single backslash. Any of these characters would end the path
	// segment early or start a new one, moving the request target.
	return !strings.ContainsAny(s, "/\\?#")
}
