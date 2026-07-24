// Copyright (c) Microsoft. All rights reserved.

package main

import "time"

var exampleSets = map[string][]ExampleDefinition{
	"01-get-started": getStartedExamples,
	"02-agents":      agentsExamples,
	"03-workflows":   workflowExamples,
}

var getStartedExamples = []ExampleDefinition{
	{
		Name:                         "01_get_started_01_hello_agent",
		ProjectPath:                  "examples/01-get-started/01_hello_agent",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain a joke about a pirate.",
			"There should be two separate joke responses — one from a non-streaming call and one from a streaming call.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "01_get_started_02_add_tools",
		ProjectPath:                  "examples/01-get-started/02_add_tools",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain information about the weather in Amsterdam.",
			"The response should mention that it is cloudy with a high of 15°C (or equivalent), since this comes from a tool that returns a canned response.",
			"There should be two responses — one from a non-streaming call and one from a streaming call.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "01_get_started_03_multi_turn",
		ProjectPath:                  "examples/01-get-started/03_multi_turn",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain a joke about a pirate.",
			"After the initial joke, there should be a modified version that includes emojis and is told in the voice of a pirate's parrot.",
			"The pattern repeats: first a non-streaming pirate joke + parrot version, then a streaming pirate joke + parrot version.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "01_get_started_04_memory",
		ProjectPath:                  "examples/01-get-started/04_memory",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		MustContain: []string{
			">> Use session with blank memory",
			">> Use deserialized session with previously created memories",
			">> Read memories using memory component",
			"MEMORY - User Name:",
			"MEMORY - User Age:",
			">> Use new session with previously created memories",
		},
		ExpectedOutputDescription: []string{
			"In the blank-memory section, the agent should respond to the user's messages and may ask for missing name or age details.",
			"After the session is serialized and deserialized, the agent should recall the user's name and age.",
			"The memory readout should include the captured user name and age.",
			"In the new-session section, the agent should know the user's name and age from the transferred memory.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:            "01_get_started_05_first_workflow",
		ProjectPath:     "examples/01-get-started/05_first_workflow",
		IsDeterministic: true,
		MustContain: []string{
			"Input: \"Hello, World!\"",
			"UppercaseExecutor: HELLO, WORLD!",
			"ReverseExecutor: !DLROW ,OLLEH",
		},
	},
}

var agentsExamples = []ExampleDefinition{
	{
		Name:                         "02_agents_agents_step01_running",
		ProjectPath:                  "examples/02-agents/agents/step01_running",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain two separate joke responses about a pirate.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_agents_step02_multiturn_conversation",
		ProjectPath:                  "examples/02-agents/agents/step02_multiturn_conversation",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain a pirate joke and a later version in a pirate parrot voice with emojis.",
			"The pattern should appear for both non-streaming and streaming turns.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_agents_step03_using_function_tools",
		ProjectPath:                  "examples/02-agents/agents/step03_using_function_tools",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain information about the weather in Amsterdam.",
			"The response should mention that it is cloudy with a high of 15°C (or equivalent), since this comes from a tool that returns a canned response.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_agents_step04_using_function_tools_with_approvals",
		ProjectPath:                  "examples/02-agents/agents/step04_using_function_tools_with_approvals",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		Inputs:                       inputLines("Y"),
		InputDelay:                   5 * time.Second,
		ExpectedOutputDescription: []string{
			"The output should show a tool approval request for the weather function.",
			"After approval, the agent should answer with the weather in Amsterdam.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_agents_step05_structured_output",
		ProjectPath:                  "examples/02-agents/agents/step05_structured_output",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		MustContain: []string{
			"Structured Output:",
			"Name:",
			"Age:",
			"Occupation:",
		},
		ExpectedOutputDescription: []string{
			"The output should show structured person information for John Smith.",
			"The structured output should include name, age, and occupation fields.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_agents_step06_persisted_conversation",
		ProjectPath:                  "examples/02-agents/agents/step06_persisted_conversation",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should start with a joke about a pirate.",
			"After serializing and loading the session, the second response should retell the same joke in a pirate voice with emojis, demonstrating preserved context.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_agents_step07_3rdparty_session_storage",
		ProjectPath:                  "examples/02-agents/agents/step07_3rdparty_session_storage",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		MustContain:                  []string{"--- Serialized session ---"},
		ExpectedOutputDescription: []string{
			"The output should contain a pirate joke response and a serialized session JSON block.",
			"The second response should use the persisted third-party history to continue the earlier conversation.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_agents_step08_observability",
		ProjectPath:                  "examples/02-agents/agents/step08_observability",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain a joke response from the agent and observability trace output.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_agents_step09_dependency_injection",
		ProjectPath:                  "examples/02-agents/agents/step09_dependency_injection",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		SkipReason:                   "Example is currently a TODO placeholder and produces no output.",
		ExpectedOutputDescription: []string{
			"The output should contain a joke about a pirate.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_agents_step10_as_mcp_tool",
		ProjectPath:                  "examples/02-agents/agents/step10_as_mcp_tool",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		SkipReason:                   "Runs as an MCP stdio server that does not exit on its own.",
	},
	{
		Name:                         "02_agents_agents_step11_using_images",
		ProjectPath:                  "examples/02-agents/agents/step11_using_images",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should describe an image of a nature boardwalk or walkway scene.",
			"It should mention elements like a wooden boardwalk or path, greenery or vegetation, and an outdoor natural setting.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_agents_step12_as_function_tool",
		ProjectPath:                  "examples/02-agents/agents/step12_as_function_tool",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should be a response about the weather in Amsterdam, written in French.",
			"The response should reference the tool result: cloudy weather with a high of 15°C.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_agents_step17_additional_ai_context",
		ProjectPath:                  "examples/02-agents/agents/step17_additional_ai_context",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should show a personal assistant managing a todo list across multiple turns.",
			"The assistant should acknowledge adding items like picking up milk, taking Sally to soccer practice, and making a dentist appointment for Jimmy.",
			"There should be a JSON block showing the serialized session state.",
			"The final response should reference the calendar appointments (doctor at 15:00, team meeting at 17:00, birthday party at 20:00).",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_agents_step18_compaction_pipeline",
		ProjectPath:                  "examples/02-agents/agents/step18_compaction_pipeline",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should show a turn-by-turn shopping conversation with user prompts and assistant replies.",
			"The output may include message-count lines showing chat history compaction.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_agents_step22_foundry_memory",
		ProjectPath:                  "examples/02-agents/agents/step22_foundry_memory",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL", "AZURE_AI_MEMORY_STORE_ID", "AZURE_AI_EMBEDDING_DEPLOYMENT_NAME"},
		MustContain: []string{
			"Foundry Memory",
		},
		ExpectedOutputDescription: []string{
			"The output should show Foundry memory being used across multiple agent sessions.",
			"The later response should recall facts from the earlier conversation using shared Foundry memory.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_azure_openai_chat_completion",
		ProjectPath:                  "examples/02-agents/providers/azure/openai_chat_completion",
		RequiredEnvironmentVariables: []string{"AZURE_OPENAI_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"AZURE_OPENAI_DEPLOYMENT_NAME"},
		ExpectedOutputDescription: []string{
			"The output should contain a joke about a pirate.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_azure_openai_responses",
		ProjectPath:                  "examples/02-agents/providers/azure/openai_responses",
		RequiredEnvironmentVariables: []string{"AZURE_OPENAI_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"AZURE_OPENAI_DEPLOYMENT_NAME"},
		ExpectedOutputDescription: []string{
			"The output should contain two separate joke responses about a pirate.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_azure_ai_project",
		ProjectPath:                  "examples/02-agents/providers/azure/ai_project",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain a joke about a pirate from a Foundry project-backed agent.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_azure_foundry_model",
		ProjectPath:                  "examples/02-agents/providers/azure/foundry_model",
		RequiredEnvironmentVariables: []string{"AZURE_OPENAI_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"AZURE_OPENAI_DEPLOYMENT_NAME", "FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain a joke about a pirate.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_foundry_step01_basic",
		ProjectPath:                  "examples/02-agents/providers/foundry/step01_basic",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain a joke response from the Foundry-backed agent.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_foundry_step02_1_multiturn",
		ProjectPath:                  "examples/02-agents/providers/foundry/step02_1_multiturn",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain a pirate joke and a follow-up cat-and-dog joke that uses the earlier joke as an anchor.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_foundry_step02_2_multiturn_with_server_conversations",
		ProjectPath:                  "examples/02-agents/providers/foundry/step02_2_multiturn_with_server_conversations",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain multiple joke responses showing a multi-turn server-managed conversation.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_foundry_step03_function_tools",
		ProjectPath:                  "examples/02-agents/providers/foundry/step03_function_tools",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain information about the weather in Amsterdam.",
			"The response should reference cloudy weather with a high of 15 degrees Celsius from the tool result.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_foundry_step04_function_tools_with_approvals",
		ProjectPath:                  "examples/02-agents/providers/foundry/step04_function_tools_with_approvals",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		Inputs:                       inputLines("Y", "Y", "Y"),
		InputDelay:                   3 * time.Second,
		ExpectedOutputDescription: []string{
			"The output should contain a prompt asking the user to approve a tool call, followed by weather information about Amsterdam.",
			"The response should mention cloudy weather with a high of 15°C.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_foundry_step05_structured_output",
		ProjectPath:                  "examples/02-agents/providers/foundry/step05_structured_output",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		MustContain:                  []string{"Structured Output:", "Name:", "Age:", "Occupation:"},
		ExpectedOutputDescription: []string{
			"The output should show structured person information with name, age, and occupation fields.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_foundry_step06_persisted_conversations",
		ProjectPath:                  "examples/02-agents/providers/foundry/step06_persisted_conversations",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain a pirate joke followed by a follow-up response after session serialization and deserialization.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_foundry_step07_observability",
		ProjectPath:                  "examples/02-agents/providers/foundry/step07_observability",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain a joke response from the agent and observability trace output.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_foundry_step09_mcp_client_as_tools",
		ProjectPath:                  "examples/02-agents/providers/foundry/step09_mcp_client_as_tools",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should show an agent using Microsoft Learn MCP tools to search or retrieve documentation.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_foundry_step10_images",
		ProjectPath:                  "examples/02-agents/providers/foundry/step10_images",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should describe an image of a nature walkway or boardwalk scene.",
			"It should mention elements like a wooden path, greenery, and an outdoor setting.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_foundry_step11_as_function_tool",
		ProjectPath:                  "examples/02-agents/providers/foundry/step11_as_function_tool",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should be a response about the weather in Amsterdam, written in French.",
			"The response should reference the tool result: cloudy weather with a high of 15°C.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_foundry_step12_middleware",
		ProjectPath:                  "examples/02-agents/providers/foundry/step12_middleware",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain multiple middleware examples or demonstrate middleware intercepting a harmful request before a normal agent response.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_foundry_step13_plugins",
		ProjectPath:                  "examples/02-agents/providers/foundry/step13_plugins",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain information about both the current time and the weather in Seattle.",
			"The weather information should be similar to: cloudy with a high of 15°C. Exact phrasing may vary.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_foundry_step14_code_interpreter",
		ProjectPath:                  "examples/02-agents/providers/foundry/step14_code_interpreter",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should show the code interpreter being used to solve sin(x) + x^2 = 42.",
			"It may show computed answers, code, or annotations with file references.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_foundry_step21_web_search",
		ProjectPath:                  "examples/02-agents/providers/foundry/step21_web_search",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should show an agent using web search to answer a question, with response text and citation annotations.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_foundry_step23_local_mcp",
		ProjectPath:                  "examples/02-agents/providers/foundry/step23_local_mcp",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should show an agent using the Microsoft Learn MCP server to search for documentation and provide a response.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_openai",
		ProjectPath:                  "examples/02-agents/providers/openai",
		RequiredEnvironmentVariables: []string{"OPENAI_API_KEY"},
		ExpectedOutputDescription: []string{
			"The output should contain a joke about a pirate.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_gemini",
		ProjectPath:                  "examples/02-agents/providers/gemini",
		RequiredEnvironmentVariables: []string{"GEMINI_API_KEY"},
		OptionalEnvironmentVariables: []string{"GEMINI_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should contain a joke about a pirate.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_providers_anthropic",
		ProjectPath:                  "examples/02-agents/providers/anthrophic",
		RequiredEnvironmentVariables: []string{"ANTHROPIC_API_KEY"},
		ExpectedOutputDescription: []string{
			"The output should contain a joke about a pirate.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:        "02_agents_a2a_as_function_tools",
		ProjectPath: "examples/02-agents/a2a/as_function_tools",
		SkipReason:  "Requires a running A2A example server.",
	},
	{
		Name:        "02_agents_a2a_polling_for_task_completion",
		ProjectPath: "examples/02-agents/a2a/polling_for_task_completion",
		SkipReason:  "Requires a running A2A example server.",
	},
	{
		Name:        "02_agents_a2a_protocol_selection",
		ProjectPath: "examples/02-agents/a2a/protocol_selection",
		SkipReason:  "Requires a running A2A example server.",
	},
	{
		Name:        "02_agents_a2a_stream_reconnection",
		ProjectPath: "examples/02-agents/a2a/stream_reconnection",
		SkipReason:  "Requires a running A2A example server.",
	},
	{
		Name:        "02_agents_providers_a2a",
		ProjectPath: "examples/02-agents/providers/a2a",
		SkipReason:  "Requires a running A2A example server.",
	},
	{
		Name:        "02_agents_agui_step01_getting_started_client",
		ProjectPath: "examples/02-agents/agui/step01_getting_started/client",
		SkipReason:  "Requires a running AGUI example server.",
	},
	{
		Name:                         "02_agents_agui_step01_getting_started_server",
		ProjectPath:                  "examples/02-agents/agui/step01_getting_started/server",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		SkipReason:                   "Starts an AGUI web server that does not exit.",
	},
	{
		Name:        "02_agents_agui_step02_backend_tools_client",
		ProjectPath: "examples/02-agents/agui/step02_backend_tools/client",
		SkipReason:  "Requires a running AGUI example server.",
	},
	{
		Name:                         "02_agents_agui_step02_backend_tools_server",
		ProjectPath:                  "examples/02-agents/agui/step02_backend_tools/server",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		SkipReason:                   "Starts an AGUI web server that does not exit.",
	},
	{
		Name:        "02_agents_agui_step03_frontend_tools_client",
		ProjectPath: "examples/02-agents/agui/step03_frontend_tools/client",
		SkipReason:  "Requires a running AGUI example server.",
	},
	{
		Name:                         "02_agents_agui_step03_frontend_tools_server",
		ProjectPath:                  "examples/02-agents/agui/step03_frontend_tools/server",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		SkipReason:                   "Starts an AGUI web server that does not exit.",
	},
	{
		Name:        "02_agents_agui_step04_human_in_loop_client",
		ProjectPath: "examples/02-agents/agui/step04_human_in_loop/client",
		SkipReason:  "Requires a running AGUI example server.",
	},
	{
		Name:                         "02_agents_agui_step04_human_in_loop_server",
		ProjectPath:                  "examples/02-agents/agui/step04_human_in_loop/server",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		SkipReason:                   "Starts an AGUI web server that does not exit.",
	},
	{
		Name:        "02_agents_agui_step05_state_management_client",
		ProjectPath: "examples/02-agents/agui/step05_state_management/client",
		SkipReason:  "Requires a running AGUI example server.",
	},
	{
		Name:                         "02_agents_agui_step05_state_management_server",
		ProjectPath:                  "examples/02-agents/agui/step05_state_management/server",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		SkipReason:                   "Starts an AGUI web server that does not exit.",
	},
	{
		Name:                         "02_agents_mcp_agent_mcp_server",
		ProjectPath:                  "examples/02-agents/mcp/agent_mcp_server",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		SkipReason:                   "Starts an MCP server that does not exit.",
	},
	{
		Name:        "02_agents_providers_github_copilot",
		ProjectPath: "examples/02-agents/providers/github-copilot",
		Inputs:      inputLines("Y", "Y", "Y"),
		InputDelay:  3 * time.Second,
		ExpectedOutputDescription: []string{
			"The output should contain a response listing files in the current directory.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_skills_step01_file_based_skills",
		ProjectPath:                  "examples/02-agents/skills/step01_file_based_skills",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		MustContain:                  []string{"File-Based Skills"},
		ExpectedOutputDescription: []string{
			"The output should show the agent converting 26.2 miles to kilometers and 75 kilograms to pounds.",
			"The response should contain approximate numeric values for both conversions.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_skills_step02_code_defined_skills",
		ProjectPath:                  "examples/02-agents/skills/step02_code_defined_skills",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		MustContain:                  []string{"Code-Defined Skills"},
		ExpectedOutputDescription: []string{
			"The output should show the agent converting 26.2 miles to kilometers and 75 kilograms to pounds.",
			"The response should contain approximate numeric values for both conversions.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "02_agents_skills_step03_mixed_skills",
		ProjectPath:                  "examples/02-agents/skills/step03_mixed_skills",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should show the agent using mixed file-based and code-defined skills.",
			"The response should include useful unit conversion results.",
			"The output should not contain error messages or stack traces.",
		},
	},
}

var workflowExamples = []ExampleDefinition{
	{
		Name:            "03_workflows_01_start_here_01_streaming",
		ProjectPath:     "examples/03-workflows/01-start-here/01_streaming",
		IsDeterministic: true,
		MustContain: []string{
			"UppercaseExecutor",
			"ReverseTextExecutor: !DLROW ,OLLEH",
		},
	},
	{
		Name:                         "03_workflows_01_start_here_02_agents_in_workflows",
		ProjectPath:                  "examples/03-workflows/01-start-here/02_agents_in_workflows",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should show agent responses from a translation workflow.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "03_workflows_01_start_here_03_agent_workflow_patterns",
		ProjectPath:                  "examples/03-workflows/01-start-here/03_agent_workflow_patterns",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		Inputs:                       inputLines("sequential"),
		InputDelay:                   3 * time.Second,
		ExpectedOutputDescription: []string{
			"The output should show a sequential workflow pattern with multiple agents executing tasks in order.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "03_workflows_01_start_here_04_multi_model_service",
		ProjectPath:                  "examples/03-workflows/01-start-here/04_multi_model_service",
		RequiredEnvironmentVariables: []string{"BEDROCK_ACCESS_KEY", "BEDROCK_SECRET_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"},
		SkipReason:                   "Requires multiple external provider API keys (Bedrock, Anthropic, OpenAI).",
	},
	{
		Name:            "03_workflows_01_start_here_05_subworkflow",
		ProjectPath:     "examples/03-workflows/01-start-here/05_subworkflow",
		IsDeterministic: true,
		MustContain: []string{
			"Building subworkflow: Uppercase -> Reverse -> Append Suffix",
			"Building parent workflow: Prefix -> SubWorkflow -> PostProcess",
			"Final output:",
		},
	},
	{
		Name:                         "03_workflows_01_start_here_06_mixed_workflow_agents_and_executors",
		ProjectPath:                  "examples/03-workflows/01-start-here/06_mixed_workflow_agents_and_executors",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		Inputs:                       inputLines("What is 2 plus 2?"),
		InputDelay:                   3 * time.Second,
		ExpectedOutputDescription: []string{
			"The output should show agents and executors working together to process a user question.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "03_workflows_01_start_here_07_writer_critic_workflow",
		ProjectPath:                  "examples/03-workflows/01-start-here/07_writer_critic_workflow",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		MustContain:                  []string{"Writer Critic Workflow"},
		ExpectedOutputDescription: []string{
			"The output should show a writer-critic iteration workflow with writer and critic sections.",
			"The critic should either approve or request revisions.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "03_workflows_agents_custom_agent_executors",
		ProjectPath:                  "examples/03-workflows/agents/custom_agent_executors",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should show custom workflow events including slogan generation and feedback.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "03_workflows_agents_group_chat_tool_approval",
		ProjectPath:                  "examples/03-workflows/agents/group_chat_tool_approval",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		MustContain:                  []string{"Starting group chat workflow for software deployment..."},
		ExpectedOutputDescription: []string{
			"The output should show a group chat workflow with QA and DevOps agents for software deployment.",
			"There should be approval requests for tool calls.",
			"The workflow should show interaction between QA and DevOps agents toward deployment.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:                         "03_workflows_agents_workflow_as_an_agent",
		ProjectPath:                  "examples/03-workflows/agents/workflow_as_an_agent",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should show a conversational workflow responding to user questions about city park design.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:            "03_workflows_checkpoint_checkpoint_and_rehydrate",
		ProjectPath:     "examples/03-workflows/checkpoint/checkpoint_and_rehydrate",
		IsDeterministic: true,
		MustContain: []string{
			"Workflow completed with result:",
			"Number of checkpoints created:",
			"Hydrating a new workflow instance from the 6th checkpoint.",
		},
	},
	{
		Name:            "03_workflows_checkpoint_checkpoint_and_resume",
		ProjectPath:     "examples/03-workflows/checkpoint/checkpoint_and_resume",
		IsDeterministic: true,
		MustContain: []string{
			"Workflow completed with result:",
			"Number of checkpoints created:",
			"Restoring from the 6th checkpoint.",
		},
	},
	{
		Name:        "03_workflows_checkpoint_checkpoint_with_human_in_the_loop",
		ProjectPath: "examples/03-workflows/checkpoint/checkpoint_with_human_in_the_loop",
		Inputs:      inputLines("50", "25", "40", "45", "42", "50", "25", "40", "45", "42"),
		InputDelay:  time.Second,
		MustContain: []string{
			"Workflow completed with result:",
			"Number of checkpoints created:",
			"Restored run completed with result:",
		},
		ExpectedOutputDescription: []string{
			"The output should show a number guessing workflow with higher/lower hints that eventually reaches the correct number.",
			"The output should demonstrate checkpoint save and restore behavior.",
		},
	},
	{
		Name:        "03_workflows_concurrent_map_reduce",
		ProjectPath: "examples/03-workflows/concurrent/map_reduce",
		MustContain: []string{"Map Reduce Workflow", "reduced_results_reduce_executor_"},
	},
	{
		Name:                         "03_workflows_concurrent_concurrent",
		ProjectPath:                  "examples/03-workflows/concurrent/concurrent",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		ExpectedOutputDescription: []string{
			"The output should show results from concurrent agent processing.",
			"The output should not contain error messages or stack traces.",
		},
	},
	{
		Name:            "03_workflows_conditional_edges_01_edge_condition",
		ProjectPath:     "examples/03-workflows/conditional-edges/01_edge_condition",
		IsDeterministic: true,
		MustContain:     []string{"Email marked as spam: matched suspicious wording"},
	},
	{
		Name:            "03_workflows_conditional_edges_02_switch_case",
		ProjectPath:     "examples/03-workflows/conditional-edges/02_switch_case",
		IsDeterministic: true,
		MustContain:     []string{"Email queued for review: needs manual invoice review"},
	},
	{
		Name:            "03_workflows_conditional_edges_03_multi_selection",
		ProjectPath:     "examples/03-workflows/conditional-edges/03_multi_selection",
		IsDeterministic: true,
		MustContain:     []string{"Email sent:", "Logged: Summary:"},
	},
	{
		Name:        "03_workflows_human_in_the_loop_human_in_the_loop_basic",
		ProjectPath: "examples/03-workflows/human-in-the-loop/human_in_the_loop_basic",
		Inputs:      inputLines("50", "25", "40", "45", "42"),
		InputDelay:  time.Second,
		MustContain: []string{"found in"},
		ExpectedOutputDescription: []string{
			"The output should show a number guessing game with higher/lower hints that eventually reaches the correct number 42.",
		},
	},
	{
		Name:        "03_workflows_loop",
		ProjectPath: "examples/03-workflows/loop",
		MustContain: []string{"found in"},
	},
	{
		Name:            "03_workflows_message_workflow",
		ProjectPath:     "examples/03-workflows/message-workflow",
		IsDeterministic: true,
		MustContain: []string{
			"You said: hello | how are you?",
			"forwarded turn tokens: 1",
			"forwarded turn tokens: 0",
			"TakeTurnHandler invocations: 1",
		},
	},
	{
		Name:            "03_workflows_shared_states",
		ProjectPath:     "examples/03-workflows/shared-states",
		IsDeterministic: true,
		MustContain: []string{
			"Total Paragraphs:",
			"Total Words:",
		},
	},
	{
		Name:                         "03_workflows_observability_workflow_as_an_agent",
		ProjectPath:                  "examples/03-workflows/observability/workflow_as_an_agent",
		RequiredEnvironmentVariables: []string{"FOUNDRY_PROJECT_ENDPOINT"},
		OptionalEnvironmentVariables: []string{"FOUNDRY_MODEL"},
		SkipReason:                   "Interactive console with ReadLine loop; requires OTLP endpoint.",
	},
	{
		Name:            "03_workflows_subworkflows_nested_order_processing",
		ProjectPath:     "examples/03-workflows/subworkflows/nested_order_processing",
		IsDeterministic: true,
		MustContain: []string{
			"Nested Sub-Workflows",
			"Starting order processing",
			"Order completed:",
		},
	},
}
