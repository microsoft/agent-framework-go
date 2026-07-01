![Microsoft Agent Framework](docs/assets/readme-banner.png)

# Welcome to Microsoft Agent Framework for Go!

[![Microsoft Foundry Discord](https://dcbadge.limes.pink/api/server/b5zjErwbQM?style=flat)](https://discord.gg/b5zjErwbQM)
[![MS Learn Documentation](https://img.shields.io/badge/MS%20Learn-Documentation-blue)](https://learn.microsoft.com/en-us/agent-framework/)
[![Go Reference](https://pkg.go.dev/badge/github.com/microsoft/agent-framework-go.svg)](https://pkg.go.dev/github.com/microsoft/agent-framework-go)
[![GitHub stars](https://img.shields.io/github/stars/microsoft/agent-framework-go?style=social)](https://github.com/microsoft/agent-framework-go/stargazers)

Microsoft Agent Framework (MAF) is an open, multi-language framework for building **production-grade AI agents and multi-agent workflows**. This repository contains the Go implementation of Microsoft Agent Framework, along with Go samples and documentation to help you get started.

Microsoft Agent Framework is built for teams taking agents from prototype to production. It provides a consistent foundation for building, orchestrating, and operating agent systems while keeping architecture choices open as requirements evolve, and supports a broad ecosystem including Microsoft Foundry, Azure OpenAI, OpenAI, Model Context Protocol (MCP), Agent2Agent (A2A), AG-UI, and the GitHub Copilot SDK.

The .NET and Python implementations are available in the [upstream Microsoft Agent Framework repository](https://github.com/microsoft/agent-framework).

> **Note:** Microsoft Agent Framework for Go is in public preview and is currently evolving outside the core upstream codebase. As adoption and feedback grow, we expect closer alignment with the broader MAF ecosystem.

<p align="center">
  <a href="https://www.youtube.com/watch?v=AAgdMhftj8w" title="Watch the full Agent Framework introduction (30 min)">
    <img src="https://img.youtube.com/vi/AAgdMhftj8w/hqdefault.jpg"
         alt="Watch the full Agent Framework introduction (30 min)" width="480">
  </a>
</p>
<p align="center">
  <a href="https://www.youtube.com/watch?v=AAgdMhftj8w">
    Watch the full Agent Framework introduction (30 min)
  </a>
</p>

## Is this the right framework for you?

MAF is a strong fit if you:

- are building agents and workflows you expect to run in production,
- need orchestration beyond a single prompt or stateless chat loop,
- want graph-based patterns such as sequential, concurrent, group collaboration, and custom workflow routing,
- care about checkpointing, restartability, observability, governance, or human-in-the-loop control,
- need provider flexibility so your architecture can evolve without major rewrites.

## Key Features

Explore new MAF capabilities and real implementation patterns on the [official blog](https://devblogs.microsoft.com/agent-framework/).

For a detailed .NET-to-Go feature comparison, see the [.NET and Go SDK feature comparison](./docs/dotnet-go-sdk-feature-comparison.md). In short, the Go SDK covers core agents, tools, middleware, workflows, observability, and interoperability integrations, while .NET currently has broader product integrations and several features that are not implemented yet in Go.

- **Go Support**: Go packages, examples, and APIs for building agents and workflows in Go.
  - [Go reference](https://pkg.go.dev/github.com/microsoft/agent-framework-go) | [Go examples](./examples/)
- **Multiple Agent Provider Support**: Support for various LLM and agent providers, with more being added continuously.
  - [Provider examples](./examples/02-agents/providers/) | [Provider packages](./provider/)
- **Middleware**: Flexible middleware for request/response processing, logging, OpenTelemetry, context providers, tool approval, and automatic tool calling.
  - [Agent middleware](./agent/middleware.go) | [Agent harness](./agent/harness/)
- **Orchestration Patterns & Workflows**: Build multi-agent systems with graph-based workflows supporting sequential, concurrent, group collaboration, conditional routing, subworkflows, checkpointing, streaming, human-in-the-loop, and time-travel patterns. Handoff orchestration is not implemented yet in the Go SDK.
  - [Workflow examples](./examples/03-workflows/) | [Workflow package](./workflow/)
- **Microsoft Foundry Agents**: Build project-backed Foundry agents, invoke existing server-side Foundry agents, pass Foundry client headers, capture served-model metadata, and use Foundry memory. Foundry-hosted deployment is not implemented yet in the Go SDK.
  - [Foundry examples](./examples/02-agents/providers/foundry/) | [Foundry provider](./provider/foundryprovider/)
- **Observability**: OpenTelemetry integration for distributed tracing, monitoring, and debugging.
  - [Agent OpenTelemetry](./provider/otelprovider/) | [Workflow OpenTelemetry](./workflow/observability/opentelemetry/)
- **Declarative Agents**: Not implemented yet in the Go SDK.
- **Agent Skills**: Build domain-specific knowledge bases from files, inline definitions, and scripts for agents to discover and use.
  - [Skills examples](./examples/02-agents/skills/) | [Skills package](./agent/skills/)
- **AF Labs**: Not implemented yet in the Go SDK.
- **DevUI**: Not implemented yet in the Go SDK.

## Table of Contents

- [Welcome to Microsoft Agent Framework for Go!](#welcome-to-microsoft-agent-framework-for-go)
	- [Is this the right framework for you?](#is-this-the-right-framework-for-you)
	- [Key Features](#key-features)
	- [Table of Contents](#table-of-contents)
	- [Getting Started](#getting-started)
		- [Installation](#installation)
		- [Learning Resources](#learning-resources)
		- [Quickstart](#quickstart)
			- [Basic Agent - Go](#basic-agent---go)
	- [More Examples \& Samples](#more-examples--samples)
		- [Go](#go)
	- [Community \& Feedback](#community--feedback)
	- [Troubleshooting](#troubleshooting)
		- [Authentication](#authentication)
		- [Environment Variables](#environment-variables)
	- [Contributor Resources](#contributor-resources)
	- [Important Notes](#important-notes)
		- [Telemetry and data collection](#telemetry-and-data-collection)
		- [Preview status](#preview-status)
		- [Trademarks](#trademarks)

## Getting Started

### Installation

Go

```bash
go get github.com/microsoft/agent-framework-go
```

### Learning Resources

- **[Overview](https://learn.microsoft.com/agent-framework/overview/agent-framework-overview)** - High level overview of the framework
- **[Quick Start](https://learn.microsoft.com/agent-framework/tutorials/quick-start)** - Get started with a simple agent
- **[Tutorials](https://learn.microsoft.com/agent-framework/tutorials/overview)** - Step by step tutorials
- **[User Guide](https://learn.microsoft.com/en-us/agent-framework/user-guide/overview)** - In-depth user guide for building agents and workflows

### Quickstart

#### Basic Agent - Go

Create a simple Microsoft Foundry agent that writes a haiku about the Microsoft Agent Framework.

```go
package main

import (
	"cmp"
	"context"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
)

func main() {
	endpoint := os.Getenv("FOUNDRY_PROJECT_ENDPOINT")
	model := cmp.Or(os.Getenv("FOUNDRY_MODEL"), "gpt-4o-mini")

	// Authenticate to Microsoft Foundry.
	token, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		panic(err)
	}

	// Create a Microsoft Foundry agent.
	a := foundryprovider.NewAgent(endpoint, token, foundryprovider.ModelDeployment(model),
		foundryprovider.AgentConfig{
			Instructions: "You are a helpful assistant.",
		},
	)

	// Run the agent.
	ctx := context.Background()
	fmt.Println(a.RunText(ctx, "Write a haiku about the Microsoft Agent Framework").Collect())
}
```

## More Examples & Samples

### Go

- [Getting Started](./examples/01-get-started): progressive tutorial from hello world to workflows
- [Agent Concepts](./examples/02-agents): deep-dive samples by topic, including tools, middleware, providers, observability, A2A, AG-UI, MCP, and skills
- [Workflows](./examples/03-workflows): multi-agent patterns, routing, checkpointing, observability, and workflow orchestration
- [End-to-End](./examples/05-end-to-end): full applications and demos
- [Feature Comparison](./docs/dotnet-go-sdk-feature-comparison.md): .NET and Go SDK feature parity notes

## Community & Feedback

- **Found a bug?** File a [GitHub issue](https://github.com/microsoft/agent-framework-go/issues) to help us improve.
- **Enjoying MAF for Go?** [![GitHub stars](https://img.shields.io/badge/Star-us%20on%20GitHub-yellow)](https://github.com/microsoft/agent-framework-go) to show your support and help others discover the project.
- **Have questions?** Join our [Discord](https://discord.gg/b5zjErwbQM).

## Troubleshooting

### Authentication

| Problem | Cause | Fix |
| --- | --- | --- |
| Authentication errors when using Azure credentials | Not signed in to Azure CLI or another configured credential source | Run `az login` before starting your app, or configure the specific credential your app uses |
| API key errors | Wrong or missing API key | Verify the key and ensure it is for the correct resource or provider |
| Provider endpoint errors | Missing or incorrect endpoint, deployment, model, or API version | Check the environment variables and constructor options used by the sample or provider |

> **Tip:** `DefaultAzureCredential` is convenient for development but in production, consider using a specific credential, such as managed identity, to avoid latency issues, unintended credential probing, and potential security risks from fallback mechanisms.

### Environment Variables

For environment variable configuration specific to each sample, refer to the README or source in the sample directory under [examples](./examples/).

## Contributor Resources

- [Contributing Guide](./CONTRIBUTING.md)
- [Code of Conduct](./CODE_OF_CONDUCT.md)
- [Security Policy](./SECURITY.md)
- [Support Policy](./SUPPORT.md)
- [Responsible AI Transparency FAQ](./TRANSPARENCY_FAQ.md)

## Important Notes

> [!IMPORTANT]
> If you use Microsoft Agent Framework to build applications that operate with any third-party servers, agents, code, or non-Azure Direct models ("Third-Party Systems"), you do so at your own risk. Third-Party Systems are Non-Microsoft Products under the Microsoft Product Terms and are governed by their own third-party license terms. You are responsible for any usage and associated costs.
>
> We recommend reviewing all data being shared with and received from Third-Party Systems and being cognizant of third-party practices for handling, sharing, retention, and location of data. It is your responsibility to manage whether your data will flow outside of your organization's Azure compliance and geographic boundaries and any related implications, and that appropriate permissions, boundaries, and approvals are provisioned.
>
> You are responsible for carefully reviewing and testing applications you build using Microsoft Agent Framework in the context of your specific use cases, and making all appropriate decisions and customizations. This includes implementing your own responsible AI mitigations such as metaprompt, content filters, or other safety systems, and ensuring your applications meet appropriate quality, reliability, security, and trustworthiness standards. See also: [Transparency FAQ](./TRANSPARENCY_FAQ.md).

### Telemetry and data collection

This repository does not configure Microsoft telemetry collection by default. Some packages and samples include optional OpenTelemetry instrumentation or connect to Microsoft-hosted services. To turn off framework instrumentation, do not configure OpenTelemetry exporters or telemetry middleware in your application. Service telemetry, if any, is governed by the services you choose to call.

### Preview status

The Agent Framework for Go is in public preview. Declarative agents, RAG, CodeAct, and functional workflows are not yet available. File issues on GitHub at https://github.com/microsoft/agent-framework-go/issues.

### Trademarks

This project may contain trademarks or logos for projects, products, or services. Authorized use of Microsoft trademarks or logos is subject to and must follow [Microsoft's Trademark & Brand Guidelines](https://www.microsoft.com/en-us/legal/intellectualproperty/trademarks/usage/general). Use of Microsoft trademarks or logos in modified versions of this project must not cause confusion or imply Microsoft sponsorship. Any use of third-party trademarks or logos is subject to those third-party policies.
