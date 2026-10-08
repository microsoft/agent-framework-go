// Copyright (c) Microsoft. All rights reserved.

package a2aprovider

import (
	"cmp"
	"context"
	"errors"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/microsoft/agent-framework-go/agent"
)

// NewAgentFromCard creates an agent using the card's supported interfaces.
// Empty Name and Description fields in config default to the card's values;
// other agent configuration is preserved. Neither card nor config is modified.
// The card is assumed to be trusted; use [NewAgentFromResolver] for discovery.
// By default HTTP+JSON is preferred over JSON-RPC. Client options are applied
// after this default and can customize transports, HTTP clients and preferences.
// Transports must not require client destruction: this constructor does not
// expose the created client. For resource-owning transports, create the client
// with a2aclient.NewFromCard, pass it to [NewAgent], and retain it for Destroy.
func NewAgentFromCard(ctx context.Context, card *a2a.AgentCard, config AgentConfig, options ...a2aclient.FactoryOption) (*agent.Agent, error) {
	if card == nil {
		return nil, errors.New("a2aprovider: agent card cannot be nil")
	}
	defaults := []a2aclient.FactoryOption{a2aclient.WithConfig(a2aclient.Config{
		PreferredTransports: []a2a.TransportProtocol{a2a.TransportProtocolHTTPJSON, a2a.TransportProtocolJSONRPC},
	})}
	client, err := a2aclient.NewFromCard(ctx, card, append(defaults, options...)...)
	if err != nil {
		return nil, err
	}
	config.Name = cmp.Or(config.Name, card.Name)
	config.Description = cmp.Or(config.Description, card.Description)
	return NewAgent(client, config), nil
}

// NewAgentFromResolver resolves the public agent card at baseURL and creates an
// agent using [NewAgentFromCard]. The resolver uses its own HTTP client to fetch
// the card; client options configure communication with the resolved agent, not
// discovery. Discovery and client creation errors are returned unchanged.
// The transport ownership requirements of [NewAgentFromCard] also apply here.
func NewAgentFromResolver(ctx context.Context, resolver *agentcard.Resolver, baseURL string, config AgentConfig, options ...a2aclient.FactoryOption) (*agent.Agent, error) {
	if resolver == nil {
		return nil, errors.New("a2aprovider: card resolver cannot be nil")
	}
	card, err := resolver.Resolve(ctx, baseURL)
	if err != nil {
		return nil, err
	}
	return NewAgentFromCard(ctx, card, config, options...)
}
