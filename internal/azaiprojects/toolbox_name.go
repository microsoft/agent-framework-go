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
