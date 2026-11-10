// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/provider/foundryprovider"
	"github.com/microsoft/agent-framework-go/tool/agenttool"
	"golang.org/x/sync/errgroup"
)

//go:embed index.html
var page []byte

func main() {
	project := strings.TrimSpace(os.Getenv("FOUNDRY_PROJECT_ENDPOINT"))
	model := strings.TrimSpace(os.Getenv("FOUNDRY_MODEL"))
	if project == "" || model == "" {
		log.Fatal("FOUNDRY_PROJECT_ENDPOINT and FOUNDRY_MODEL are required")
	}
	endpoint, err := voiceEndpoint(project)
	if err != nil {
		log.Fatal(err)
	}
	credential, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		log.Fatal(err)
	}
	a := foundryprovider.NewAgent(project, credential, foundryprovider.ModelDeployment(model), foundryprovider.AgentConfig{
		Instructions: `Answer briefly using these fictional store policies and order details.
DEMO-1001: a backpack, shipped, arriving in two business days.
DEMO-1002: a rain jacket, delivered yesterday.
Returns are accepted within 30 days of delivery, with free return shipping.`,
		DisableStoreOutput: true,
		Config: agent.Config{
			Name:        "store_support",
			Description: "Answer order, shipping, and returns questions about the fictional demo store.",
		},
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	server := &http.Server{
		Handler:           newHandler(endpoint, credential, a),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("Open http://%s", listener.Addr())
	log.Fatal(server.Serve(listener))
}

func newHandler(endpoint string, credential azcore.TokenCredential, a *agent.Agent) http.Handler {
	delegate := agenttool.New(a, agenttool.Config{})
	session := map[string]any{
		"instructions": `Keep spoken answers short. For store questions, call the
support tool with a self-contained query, including the order ID and relevant context.
The store and its data are fictional.`,
		"modalities": []string{"text", "audio"},
		"voice":      map[string]any{"type": "azure-standard", "name": "en-US-Ava:DragonHDLatestNeural"},
		"turn_detection": map[string]any{
			"type": "azure_semantic_vad", "create_response": false,
			"interrupt_response": true, "auto_truncate": true,
		},
		"tools": []any{map[string]any{
			"type": "function", "name": delegate.Name(),
			"description": delegate.Description(), "parameters": delegate.Schema(),
		}},
	}
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if _, err := w.Write(page); err != nil {
			log.Printf("write page: %v", err)
		}
	})
	mux.HandleFunc("GET /session", func(w http.ResponseWriter, r *http.Request) {
		browser, err := websocket.Accept(w, r, nil)
		if err != nil {
			log.Printf("accept control connection: %v", err)
			return
		}
		defer closeSocket(browser)
		browser.SetReadLimit(64 << 10)
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		err = func() error {
			_, offer, err := browser.Read(ctx)
			if err != nil {
				return err
			}
			token, err := credential.GetToken(ctx, policy.TokenRequestOptions{
				Scopes: []string{"https://ai.azure.com/.default"},
			})
			if err != nil {
				return err
			}
			upstream, _, err := websocket.Dial(ctx, endpoint, &websocket.DialOptions{ //nolint:bodyclose // Dial owns the handshake response body.
				HTTPClient: client,
				HTTPHeader: http.Header{"Authorization": {"Bearer " + token.Token}},
			})
			if err != nil {
				return fmt.Errorf("connect Voice Live: %w", err)
			}
			defer closeSocket(upstream)
			upstream.SetReadLimit(1 << 20)
			if err := wsjson.Write(ctx, upstream, map[string]any{
				"type": "rtc.call.sdp.create", "sdp_offer": string(offer), "session": session,
			}); err != nil {
				return err
			}
			cancel()
			group, ctx := errgroup.WithContext(r.Context())
			group.Go(func() error { return forward(ctx, browser, upstream) })
			group.Go(func() error { return forward(ctx, upstream, browser) })
			return group.Wait()
		}()
		if err == nil || errors.Is(err, context.Canceled) {
			return
		}
		switch websocket.CloseStatus(err) {
		case websocket.StatusNormalClosure, websocket.StatusGoingAway:
			return
		}
		log.Printf("Voice Live control: %v", err)
		writeCtx, writeCancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer writeCancel()
		if err := wsjson.Write(writeCtx, browser, map[string]any{
			"type": "error", "error": map[string]any{"message": err.Error()},
		}); err != nil {
			log.Printf("write control error: %v", err)
		}
	})
	mux.HandleFunc("POST /tool", func(w http.ResponseWriter, r *http.Request) {
		args, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := delegate.Call(r.Context(), string(args))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(result); err != nil {
			log.Printf("write MAF result: %v", err)
		}
	})
	return http.NewCrossOriginProtection().Handler(mux)
}

func forward(ctx context.Context, from, to *websocket.Conn) error {
	for {
		kind, data, err := from.Read(ctx)
		if err != nil {
			return err
		}
		if err := to.Write(ctx, kind, data); err != nil {
			return err
		}
	}
}

func closeSocket(conn *websocket.Conn) {
	if err := conn.Close(websocket.StatusNormalClosure, ""); err != nil {
		log.Printf("close control connection: %v", err)
	}
}

func voiceEndpoint(project string) (string, error) {
	endpoint, err := url.Parse(project)
	if err != nil {
		return "", fmt.Errorf("parse FOUNDRY_PROJECT_ENDPOINT: %w", err)
	}
	if endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil ||
		!strings.HasPrefix(endpoint.Path, "/api/projects/") || endpoint.Path == "/api/projects/" ||
		endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return "", errors.New("FOUNDRY_PROJECT_ENDPOINT must be an HTTPS project endpoint, such as https://RESOURCE.services.ai.azure.com/api/projects/PROJECT")
	}
	endpoint.Scheme = "wss"
	endpoint.Path = "/voice-live/realtime/calls"
	endpoint.RawQuery = url.Values{"api-version": {"2026-07-15"}, "model": {"gpt-realtime"}}.Encode()
	return endpoint.String(), nil
}
