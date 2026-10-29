// Copyright (c) Microsoft. All rights reserved.

package foundryprovider

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/azure"
	"github.com/openai/openai-go/v3/option"
)

// NewOpenAIClient creates an OpenAI SDK client for a Foundry resource or project
// endpoint. It appends /openai/v1/. Authentication follows [NewAgent]: use a non-nil
// credential for Microsoft Entra authentication, or a nil credential with
// [azure.WithAPIKey] in options for API-key authentication.
//
// Use a resource endpoint for model APIs such as embeddings, or a project endpoint
// for project-scoped operations. Azure OpenAI deployment routing and the api-version
// query are not used for native OpenAI operations.
//
// Options configure the SDK client; Foundry-owned endpoint and protocol settings
// take precedence. Per-request options can select the project
// root and api-version for project administration through the SDK's HTTP methods.
// The client does not take ownership of the credential or caller-supplied transport.
//
// The endpoint must be an absolute Foundry URL; invalid endpoints panic.
// The Azure SDK requires HTTPS for authenticated requests.
func NewOpenAIClient(endpoint string, credential azcore.TokenCredential, options ...option.RequestOption) openai.Client {
	endpoint = normalizeAbsoluteEndpoint(endpoint)
	options = foundryOpenAIOptions(projectOpenAIBaseURL(endpoint), credential, options)
	options = append(options, option.WithQueryDel("api-version"))
	return openai.NewClient(options...)
}

func foundryOpenAIOptions(baseURL string, credential azcore.TokenCredential, options []option.RequestOption) []option.RequestOption {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		panic(fmt.Sprintf("invalid base URL %q: %v", baseURL, err))
	}
	basePath := strings.TrimRight(parsed.EscapedPath(), "/")
	result := make([]option.RequestOption, 0, len(options)+4)
	result = append(result, options...)
	result = append(result,
		// Azure authentication requires WithEndpoint's marker and guards.
		// Foundry uses the complete OpenAI base URL, not Azure deployment routing.
		azure.WithEndpoint(baseURL, foundryDataPlaneAPIVersion),
		option.WithBaseURL(baseURL),
		option.WithMiddleware(func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
			originalPath := req.URL.EscapedPath()
			path := removeAzureOpenAIPrefix(originalPath, basePath)
			// Project administration requests can select the project root with a
			// per-request base URL, outside the default OpenAI v1 route.
			if path == originalPath {
				if projectPath, ok := strings.CutSuffix(basePath, "/openai/v1"); ok {
					path = removeAzureOpenAIPrefix(path, projectPath)
				}
			}
			decoded, err := url.PathUnescape(path)
			if err != nil {
				return nil, err
			}
			req.URL.Path, req.URL.RawPath = decoded, path
			return next(req)
		}),
	)
	if credential != nil {
		result = append(result, azure.WithTokenCredential(credential, azure.WithTokenCredentialScopes([]string{azureAIResourceScope})))
	}
	return result
}

// Paths are escaped so a slash in a deployment or agent name remains one segment.
func removeAzureOpenAIPrefix(path, basePath string) string {
	if basePath != "" && path == basePath+"/openai" {
		return basePath
	}
	if relative, ok := strings.CutPrefix(path, basePath+"/openai/"); ok {
		if deployment, ok := strings.CutPrefix(relative, "deployments/"); ok {
			if _, route, ok := strings.Cut(deployment, "/"); ok {
				relative = route
			}
		}
		return basePath + "/" + relative
	}
	if path == "/openai" {
		return "/"
	}
	if relative, ok := strings.CutPrefix(path, "/openai/"); ok {
		return "/" + relative
	}
	return path
}
