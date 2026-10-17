// Copyright (c) Microsoft. All rights reserved.

package azureprovider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

const apiVersion = "2025-09-01"

type serviceClient struct {
	endpoint *url.URL
	client   *azcore.Client
}

func newServiceClient(endpoint string, config AISearchConfig) (*serviceClient, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return nil, fmt.Errorf("azureaisearch: invalid endpoint: %w", err)
	}
	if parsed.Host == "" || parsed.Scheme != "https" && parsed.Scheme != "http" ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		parsed.Path != "" && parsed.Path != "/" {
		return nil, fmt.Errorf("azureaisearch: endpoint must be an absolute HTTP or HTTPS service URL without credentials, path, query, or fragment")
	}
	if (config.Credential == nil) == (config.APIKey == nil) {
		return nil, fmt.Errorf("azureaisearch: set exactly one of Credential and APIKey")
	}
	var auth policy.Policy
	if config.APIKey != nil {
		auth = runtime.NewKeyCredentialPolicy(config.APIKey, "api-key", &runtime.KeyCredentialPolicyOptions{
			InsecureAllowCredentialWithHTTP: config.ClientOptions.InsecureAllowCredentialWithHTTP,
		})
	} else {
		audience := strings.TrimRight(config.Audience, "/")
		if audience == "" {
			audience = "https://search.azure.com"
		}
		auth = runtime.NewBearerTokenPolicy(config.Credential, []string{audience + "/.default"}, &policy.BearerTokenOptions{
			InsecureAllowCredentialWithHTTP: config.ClientOptions.InsecureAllowCredentialWithHTTP,
		})
	}
	options := config.ClientOptions
	var transport *http.Client
	switch configured := options.Transport.(type) {
	case nil:
		transport = &http.Client{}
	case *http.Client:
		clientCopy := *configured
		transport = &clientCopy
	}
	if transport != nil {
		checkRedirect := transport.CheckRedirect
		transport.CheckRedirect = func(request *http.Request, via []*http.Request) error {
			if !sameOrigin(parsed, request.URL) {
				return fmt.Errorf("azureaisearch: redirects must remain on the configured service")
			}
			if checkRedirect != nil {
				return checkRedirect(request, via)
			}
			if len(via) >= 10 {
				return fmt.Errorf("azureaisearch: stopped after 10 redirects")
			}
			return nil
		}
		options.Transport = transport
	}
	client, err := azcore.NewClient(
		"github.com/microsoft/agent-framework-go/provider/azureprovider",
		"v0.1.0",
		runtime.PipelineOptions{PerRetry: []policy.Policy{auth}},
		&options)
	if err != nil {
		return nil, err
	}
	return &serviceClient{endpoint: parsed, client: client}, nil
}

func (c *serviceClient) url(path string, query url.Values) string {
	if query == nil {
		query = make(url.Values)
	}
	query.Set("api-version", apiVersion)
	return strings.TrimRight(c.endpoint.String(), "/") + path + "?" + query.Encode()
}

func (c *serviceClient) nextURL(current, next string) (string, error) {
	base, err := url.Parse(current)
	if err != nil {
		return "", err
	}
	relative, err := url.Parse(next)
	if err != nil {
		return "", fmt.Errorf("azureaisearch: invalid continuation URL: %w", err)
	}
	resolved := base.ResolveReference(relative)
	if !c.sameOrigin(resolved) {
		return "", fmt.Errorf("azureaisearch: continuation URL must remain on the configured service")
	}
	return resolved.String(), nil
}

func (c *serviceClient) sameOrigin(target *url.URL) bool {
	return sameOrigin(c.endpoint, target)
}

func sameOrigin(base, target *url.URL) bool {
	return strings.EqualFold(target.Scheme, base.Scheme) &&
		strings.EqualFold(target.Host, base.Host) && target.User == nil && target.Fragment == ""
}

func (c *serviceClient) request(ctx context.Context, method, endpoint string, body, result any, statusCodes ...int) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := url.Parse(endpoint)
	if err != nil {
		return err
	}
	if !c.sameOrigin(target) {
		return fmt.Errorf("azureaisearch: request URL must remain on the configured service")
	}
	request, err := runtime.NewRequest(ctx, method, endpoint)
	if err != nil {
		return err
	}
	if body != nil {
		if err := runtime.MarshalAsJSON(request, body); err != nil {
			return err
		}
	}
	response, err := c.client.Pipeline().Do(request)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, response.Body.Close()) }()
	if !runtime.HasStatusCode(response, statusCodes...) {
		return runtime.NewResponseError(response)
	}
	if result == nil {
		_, err := io.Copy(io.Discard, response.Body)
		return err
	}
	return runtime.UnmarshalAsJSON(response, result)
}
