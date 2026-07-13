// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License. See License.txt in the project root for license information.

package azaiprojects

import (
	"errors"
	"net/url"
	"strings"
)

var errToolboxNameMustBeSinglePathSegment = errors.New("parameter name must resolve to a single path segment")

func replaceToolboxNamePathParameter(endpoint, urlPath, name string) (string, error) {
	if name == "" {
		return "", errors.New("parameter name cannot be empty")
	}
	if err := validateToolboxNamePathSegment(endpoint, name); err != nil {
		return "", err
	}
	return strings.ReplaceAll(urlPath, "{name}", url.PathEscape(name)), nil
}

func validateToolboxNamePathSegment(endpoint, name string) error {
	// Always reject dot-only names and names containing raw path/query/fragment
	// separators or backslashes — independent of whether a base endpoint is available.
	if name == "." || name == ".." {
		return errToolboxNameMustBeSinglePathSegment
	}
	if strings.ContainsAny(name, "/?#\\") {
		return errToolboxNameMustBeSinglePathSegment
	}
	// Reject percent-encoded separators and percent itself (catches double-encoding).
	nameLower := strings.ToLower(name)
	if strings.Contains(nameLower, "%2f") || // encoded /
		strings.Contains(nameLower, "%3f") || // encoded ?
		strings.Contains(nameLower, "%23") || // encoded #
		strings.Contains(nameLower, "%5c") || // encoded \
		strings.Contains(nameLower, "%25") { // encoded % (prevents double-encoding)
		return errToolboxNameMustBeSinglePathSegment
	}

	// When a valid absolute base URL is available, also verify via URL construction
	// that the name resolves to exactly one intact path segment.
	baseEndpoint := strings.TrimRight(endpoint, "/")
	if baseEndpoint == "" {
		return nil
	}
	baseURL, err := url.Parse(baseEndpoint)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return nil
	}

	candidate, err := url.Parse(baseEndpoint + "/toolboxes/" + name + "/mcp")
	if err != nil {
		return errToolboxNameMustBeSinglePathSegment
	}

	if candidate.Scheme != baseURL.Scheme || candidate.Host != baseURL.Host || candidate.RawQuery != "" || candidate.Fragment != "" {
		return errToolboxNameMustBeSinglePathSegment
	}

	prefix := strings.TrimRight(baseURL.Path, "/") + "/toolboxes/"
	path := candidate.Path
	const suffix = "/mcp"
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return errToolboxNameMustBeSinglePathSegment
	}

	segment := path[len(prefix) : len(path)-len(suffix)]
	if segment == "" || strings.Contains(segment, "/") || strings.Contains(segment, "\\") || segment == "." || segment == ".." || segment != name {
		return errToolboxNameMustBeSinglePathSegment
	}

	return nil
}
