// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"cmp"
	"context"
	_ "embed" // Embed import required by go:embed for []byte target
	"encoding/base64"
	"os"
	"strings"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/examples/internal/demo"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/provider/openaiprovider"
	"github.com/openai/openai-go/v3"
)

// model must support both audio and file (PDF) input. Audio input in particular
// requires an audio-capable deployment (for example "gpt-4o-audio-preview").
// Override it with OPENAI_MODEL when your deployment differs.
var model = cmp.Or(strings.TrimSpace(os.Getenv("OPENAI_MODEL")), "gpt-4o-audio-preview")

var logger = demo.NewLogger(
	"Using Audio and Files",
	"Demonstrates sending audio and PDF/file DataContent to an OpenAI chat-completions agent.",
	"Model", model,
)

//go:embed assets/sample.wav
var sampleAudio []byte

//go:embed assets/report.pdf
var reportPDF []byte

func main() {
	// The audio (input_audio) and file (file) content parts live on the OpenAI
	// chat-completions path, so this example uses NewChatCompletionsAgent rather
	// than the Responses or Foundry agents.
	a := openaiprovider.NewChatCompletionsAgent(
		openai.NewClient(),
		openaiprovider.AgentConfig{
			Model:        model,
			Instructions: "You are a helpful agent that can transcribe audio and summarize documents.",
			Config: agent.Config{
				Name:        "MultimodalAgent",
				Middlewares: []agent.Middleware{logger}, // for logging agent interactions
			},
		},
	)

	ctx := context.Background()
	msg := message.New(
		&message.TextContent{Text: "Transcribe the attached audio clip and summarize the attached PDF."},
		// audio/wav DataContent -> OpenAI input_audio content part.
		&message.DataContent{
			Name:      "sample.wav",
			Data:      base64.StdEncoding.EncodeToString(sampleAudio),
			MediaType: "audio/wav",
		},
		// application/pdf DataContent -> OpenAI file content part (file_data + filename).
		&message.DataContent{
			Name:      "report.pdf",
			Data:      base64.StdEncoding.EncodeToString(reportPDF),
			MediaType: "application/pdf",
		},
	)

	resp, err := a.RunMessage(ctx, msg).Collect()
	demo.Response(resp, err)
}
