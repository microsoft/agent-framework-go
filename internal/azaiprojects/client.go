// Copyright (c) Microsoft. All rights reserved.

package azaiprojects

import (
	"fmt"
	"reflect"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

const (
	moduleName = "github.com/microsoft/agent-framework-go/internal/azaiprojects"
	// moduleVersion doesn't need to match the containing module version, it is used for telemetry. Don't bother updating.
	moduleVersion = "v0.1.0"
)

// ServiceName is the [cloud.ServiceName] for Azure AI Foundry Projects.
const ServiceName cloud.ServiceName = moduleName

func init() {
	cloud.AzurePublic.Services[ServiceName] = cloud.ServiceConfiguration{
		Audience: "https://ai.azure.com",
	}
}

// ClientOptions contains the optional values for creating a [Client].
type ClientOptions struct {
	azcore.ClientOptions
}

// NewClient creates a new instance of Client with the specified values.
//   - endpoint - Service host.
//   - credential - Used to authorize requests. Usually a credential from azidentity.
//   - options - Contains optional client configuration. Pass nil to accept the default values.
func NewClient(endpoint string, credential azcore.TokenCredential, options *ClientOptions) (*Client, error) {
	if options == nil {
		options = &ClientOptions{}
	}
	if reflect.ValueOf(options.Cloud).IsZero() {
		options.Cloud = cloud.AzurePublic
	}
	serviceConfig, ok := options.Cloud.Services[ServiceName]
	if !ok {
		return nil, fmt.Errorf("provided Cloud field is missing configuration for %s", ServiceName)
	} else if serviceConfig.Audience == "" {
		return nil, fmt.Errorf("provided Cloud field is missing Audience for %s", ServiceName)
	}
	internal, err := azcore.NewClient(moduleName, moduleVersion, runtime.PipelineOptions{
		PerCall: []policy.Policy{
			runtime.NewBearerTokenPolicy(credential, []string{serviceConfig.Audience + "/.default"}, &policy.BearerTokenOptions{
				InsecureAllowCredentialWithHTTP: options.InsecureAllowCredentialWithHTTP,
			}),
		},
	}, &options.ClientOptions)
	if err != nil {
		return nil, err
	}
	return &Client{
		endpoint: endpoint,
		internal: internal,
	}, nil
}
