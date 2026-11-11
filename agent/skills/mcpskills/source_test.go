// Copyright (c) Microsoft. All rights reserved.

package mcpskills_test

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/skills"
	"github.com/microsoft/agent-framework-go/agent/skills/mcpskills"
	"github.com/microsoft/agent-framework-go/internal/telemetry"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/tool"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	indexURI = "skill://index.json"
	skillURI = "skill://unit-converter/SKILL.md"
	skillMD  = `---
name: unit-converter
description: Convert between common units.
---
# Unit Converter

Body content here.`
	skillIndex = `{"$schema":"https://schemas.agentskills.io/discovery/0.2.0/schema.json","skills":[
{"name":"unit-converter","type":"skill-md","description":"Convert between common units.","url":"skill://unit-converter/SKILL.md"}
]}`
)

type testServer struct {
	session    *mcp.ClientSession
	mu         sync.Mutex
	contents   map[string][]*mcp.ResourceContents
	failures   map[string]error
	requests   map[string]int
	beforeRead func(context.Context, string) error
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	state := &testServer{
		contents: make(map[string][]*mcp.ResourceContents),
		failures: make(map[string]error),
		requests: make(map[string]int),
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "skills-test", Version: "1.0.0"}, nil)
	server.AddResourceTemplate(&mcp.ResourceTemplate{
		Name:        "skills",
		URITemplate: "skill://{+path}",
	}, func(ctx context.Context, request *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		uri := request.Params.URI
		state.mu.Lock()
		state.requests[uri]++
		contents, found := state.contents[uri]
		// The MCP server fills URI and MIMEType on the returned descriptors.
		contents = slices.Clone(contents)
		for i, content := range contents {
			if content != nil {
				cloned := *content
				contents[i] = &cloned
			}
		}
		failure := state.failures[uri]
		beforeRead := state.beforeRead
		state.mu.Unlock()
		if beforeRead != nil {
			if err := beforeRead(ctx, uri); err != nil {
				return nil, err
			}
		}
		if failure != nil {
			return nil, failure
		}
		if !found {
			return nil, mcp.ResourceNotFoundError(uri)
		}
		return &mcp.ReadResourceResult{Contents: contents}, nil
	})
	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "skills-test", Version: "1.0.0"}, nil)
	state.session, err = client.Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.session.Close() })
	return state
}

func (s *testServer) set(uri string, contents ...*mcp.ResourceContents) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contents[uri] = slices.Clone(contents)
	delete(s.failures, uri)
}

func (s *testServer) text(uri, text string) {
	s.set(uri, &mcp.ResourceContents{URI: uri, Text: text})
}

func (s *testServer) blob(uri string, data []byte, mediaType string) {
	s.set(uri, &mcp.ResourceContents{URI: uri, Blob: data, MIMEType: mediaType})
}

func (s *testServer) fail(uri string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures[uri] = err
}

func (s *testServer) hook(beforeRead func(context.Context, string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.beforeRead = beforeRead
}

func (s *testServer) count(uri string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[uri]
}

func newSource(t *testing.T, server *testServer, config mcpskills.Config) *mcpskills.Source {
	t.Helper()
	config.Session = server.session
	source := mcpskills.NewSource(config)
	t.Cleanup(func() {
		if err := source.Close(); err != nil {
			t.Errorf("Close(): %v", err)
		}
	})
	return source
}

func loadSkills(t *testing.T, source skills.Source) []*skills.Skill {
	t.Helper()
	result, err := source.Skills(t.Context(), skills.SourceContext{})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func loadOne(t *testing.T, source skills.Source) *skills.Skill {
	t.Helper()
	result := loadSkills(t, source)
	if len(result) != 1 {
		t.Fatalf("Skills() returned %d skills, want 1", len(result))
	}
	return result[0]
}

func content(t *testing.T, skill *skills.Skill) string {
	t.Helper()
	result, err := skill.GetContent(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestSource_IndexBasedDiscovery_ReturnsSkill(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, skillIndex)
	server.text(skillURI, skillMD)
	source := newSource(t, server, mcpskills.Config{})

	skill := loadOne(t, source)
	if skill.Frontmatter.Name != "unit-converter" || skill.Frontmatter.Description != "Convert between common units." {
		t.Fatalf("Frontmatter = %#v", skill.Frontmatter)
	}
	if got := content(t, skill); !strings.Contains(got, "name: unit-converter") ||
		!strings.Contains(got, "description: Convert between common units.") || !strings.Contains(got, "Body content here.") {
		t.Fatalf("GetContent() = %q", got)
	}
}

func TestSource_NoIndex_ReturnsEmpty(t *testing.T) {
	server := newTestServer(t)
	server.text(skillURI, skillMD)
	source := newSource(t, server, mcpskills.Config{})
	if got := loadSkills(t, source); len(got) != 0 {
		t.Fatalf("Skills() returned %d skills without an index", len(got))
	}
}

func TestSource_SiblingText_ReturnsContent(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, skillIndex)
	uri := "skill://unit-converter/references/checklist.md"
	server.text(uri, "- check thing 1\n- check thing 2")
	skill := loadOne(t, newSource(t, server, mcpskills.Config{}))

	resource, err := skill.GetResource(t.Context(), "references/checklist.md")
	if err != nil || resource == nil {
		t.Fatalf("GetResource() = %#v, %v", resource, err)
	}
	server.text(uri, "updated")
	for range 2 {
		got, err := resource.Read(t.Context())
		if err != nil || got != "- check thing 1\n- check thing 2" {
			t.Fatalf("Read() = %#v, %v", got, err)
		}
	}
	if got := server.count(uri); got != 1 {
		t.Fatalf("resource reads = %d, want 1 prefetched response", got)
	}
}

func TestSource_SiblingBinary_ReturnsDataContent(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, skillIndex)
	uri := "skill://unit-converter/assets/icon.bin"
	server.set(uri,
		&mcp.ResourceContents{URI: uri, Text: "ignored in favor of binary"},
		&mcp.ResourceContents{URI: uri, Blob: []byte{1, 2, 3, 4}, MIMEType: "application/octet-stream", Meta: mcp.Meta{"version": "1"}},
	)
	skill := loadOne(t, newSource(t, server, mcpskills.Config{}))
	resource, err := skill.GetResource(t.Context(), "assets/icon.bin")
	if err != nil || resource == nil {
		t.Fatalf("GetResource() = %#v, %v", resource, err)
	}
	got, err := resource.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data, ok := got.(*message.DataContent)
	if !ok {
		t.Fatalf("Read() type = %T, want *message.DataContent", got)
	}
	decoded, err := data.Bytes()
	if err != nil || !bytes.Equal(decoded, []byte{1, 2, 3, 4}) || data.MediaType != "application/octet-stream" {
		t.Fatalf("binary content = %x, %q, %v", decoded, data.MediaType, err)
	}
	if data.AdditionalProperties["version"] != "1" {
		t.Fatalf("metadata = %#v", data.AdditionalProperties)
	}
}

func TestSource_UnknownResource_ReturnsNil(t *testing.T) {
	for _, test := range []struct {
		name string
		code int64
	}{
		{name: "current", code: jsonrpc.CodeInvalidParams},
		{name: "legacy", code: -32002},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newTestServer(t)
			server.text(indexURI, skillIndex)
			server.fail("skill://unit-converter/references/missing.md", &jsonrpc.Error{Code: test.code, Message: "resource not found"})
			skill := loadOne(t, newSource(t, server, mcpskills.Config{}))
			got, err := skill.GetResource(t.Context(), "references/missing.md")
			if err != nil || got != nil {
				t.Fatalf("GetResource() = %#v, %v", got, err)
			}
		})
	}
}

func TestSource_PathTraversalResource_ReturnsNil(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, skillIndex)
	skill := loadOne(t, newSource(t, server, mcpskills.Config{}))
	for _, name := range []string{"../escape.md", "references/../../escape.md", "..", "", " ", "/escape.md", `..\escape.md`} {
		t.Run(name, func(t *testing.T) {
			got, err := skill.GetResource(t.Context(), name)
			if err != nil || got != nil {
				t.Fatalf("GetResource(%q) = %#v, %v", name, got, err)
			}
		})
	}
}

func TestSource_DiscoveryDoesNotReadSkillMD(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, skillIndex)
	skill := loadOne(t, newSource(t, server, mcpskills.Config{}))
	if skill.Frontmatter.Name != "unit-converter" || server.count(skillURI) != 0 {
		t.Fatal("discovery did not rely exclusively on index metadata")
	}
	if len(skill.Scripts) != 0 || skill.GetScript != nil {
		t.Fatal("MCP skill exposed executable script lookup")
	}
}

func TestSource_InvalidIndexEntries_AreSkipped(t *testing.T) {
	for name, entry := range map[string]string{
		"invalid name":        `{"name":"UnitConverter","type":"skill-md","description":"Convert units","url":"skill://invalid/SKILL.md"}`,
		"missing name":        `{"type":"skill-md","description":"Convert units","url":"skill://invalid/SKILL.md"}`,
		"missing description": `{"name":"converter","type":"skill-md","url":"skill://invalid/SKILL.md"}`,
		"missing URL":         `{"name":"converter","type":"skill-md","description":"Convert units"}`,
		"template":            `{"name":"converter","type":"mcp-resource-template","description":"Convert units","url":"skill://{name}/SKILL.md"}`,
		"unknown type":        `{"name":"converter","type":"unknown","description":"Convert units","url":"skill://converter/SKILL.md"}`,
		"null entry":          `null`,
	} {
		t.Run(name, func(t *testing.T) {
			server := newTestServer(t)
			server.text(indexURI, `{"skills":[`+entry+`]}`)
			source := newSource(t, server, mcpskills.Config{})
			if got := loadSkills(t, source); len(got) != 0 {
				t.Fatalf("Skills() returned %d invalid skills", len(got))
			}
		})
	}
}

func TestSource_EmptyOrInvalidIndex_ReturnsEmpty(t *testing.T) {
	for _, index := range []string{"", " ", "{", "[]", "null", `{}`, `{"skills":null}`, `{"skills":"invalid"}`} {
		t.Run(index, func(t *testing.T) {
			server := newTestServer(t)
			server.text(indexURI, index)
			if got := loadSkills(t, newSource(t, server, mcpskills.Config{})); len(got) != 0 {
				t.Fatalf("Skills() returned %d skills", len(got))
			}
		})
	}
}

func TestSource_Content_CachesSuccessfulReads(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, skillIndex)
	server.text(skillURI, skillMD)
	skill := loadOne(t, newSource(t, server, mcpskills.Config{}))
	if got := content(t, skill); got != skillMD {
		t.Fatalf("GetContent() = %q", got)
	}
	server.text(skillURI, "replacement")
	if got := content(t, skill); got != skillMD || server.count(skillURI) != 1 {
		t.Fatalf("cached content = %q, reads = %d", got, server.count(skillURI))
	}
}

func TestSource_Content_JoinsTextBlocks(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, skillIndex)
	server.set(skillURI,
		&mcp.ResourceContents{URI: skillURI, Text: "first"},
		&mcp.ResourceContents{URI: skillURI, Blob: []byte("ignored")},
		&mcp.ResourceContents{URI: skillURI, Text: "second"},
	)
	if got := content(t, loadOne(t, newSource(t, server, mcpskills.Config{}))); got != "first\nsecond" {
		t.Fatalf("GetContent() = %q", got)
	}
}

func TestSource_Content_FailuresAreNotCached(t *testing.T) {
	for _, mode := range []string{"remote error", "empty text", "binary only"} {
		t.Run(mode, func(t *testing.T) {
			server := newTestServer(t)
			server.text(indexURI, skillIndex)
			switch mode {
			case "remote error":
				server.fail(skillURI, &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "read failed"})
			case "empty text":
				server.text(skillURI, "")
			case "binary only":
				server.blob(skillURI, []byte("binary"), "application/octet-stream")
			}
			skill := loadOne(t, newSource(t, server, mcpskills.Config{}))
			if _, err := skill.GetContent(t.Context()); err == nil {
				t.Fatal("GetContent() unexpectedly succeeded")
			}
			server.text(skillURI, skillMD)
			if got := content(t, skill); got != skillMD || server.count(skillURI) != 2 {
				t.Fatalf("retried content = %q, reads = %d", got, server.count(skillURI))
			}
		})
	}
}

func TestSource_Content_ConcurrentReadsShareSuccessfulFetch(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, skillIndex)
	server.text(skillURI, skillMD)
	skill := loadOne(t, newSource(t, server, mcpskills.Config{}))
	start := make(chan struct{})
	results := make(chan error, 20)
	for range 20 {
		go func() {
			<-start
			got, err := skill.GetContent(t.Context())
			if err == nil && got != skillMD {
				err = errors.New("incorrect content")
			}
			results <- err
		}()
	}
	close(start)
	for range 20 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if got := server.count(skillURI); got != 1 {
		t.Fatalf("content reads = %d, want 1", got)
	}
}

func TestSource_ResourceReadFailure_Propagates(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, skillIndex)
	server.fail("skill://unit-converter/reference.md", &jsonrpc.Error{Code: jsonrpc.CodeInternalError, Message: "upstream failure"})
	skill := loadOne(t, newSource(t, server, mcpskills.Config{}))
	_, err := skill.GetResource(t.Context(), "reference.md")
	var remote *jsonrpc.Error
	if !errors.As(err, &remote) || remote.Code != jsonrpc.CodeInternalError || remote.Message != "upstream failure" {
		t.Fatalf("GetResource() error = %v, want original remote failure", err)
	}
}

func TestSource_SiblingResource_HandlesTextEmptyAndBinaryContents(t *testing.T) {
	uri := "skill://unit-converter/reference"
	for _, test := range []struct {
		name     string
		contents []*mcp.ResourceContents
		text     string
		binary   bool
	}{
		{
			name: "multiple text blocks",
			contents: []*mcp.ResourceContents{
				{URI: uri, Text: "first"},
				{URI: uri, Text: "second"},
			},
			text: "first\nsecond",
		},
		{name: "empty response", contents: []*mcp.ResourceContents{}},
		{name: "empty text", contents: []*mcp.ResourceContents{{URI: uri}}},
		{name: "empty binary", contents: []*mcp.ResourceContents{{URI: uri, Blob: []byte{}}}, binary: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newTestServer(t)
			server.text(indexURI, skillIndex)
			server.set(uri, test.contents...)
			skill := loadOne(t, newSource(t, server, mcpskills.Config{}))
			resource, err := skill.GetResource(t.Context(), "reference")
			if err != nil || resource == nil {
				t.Fatalf("GetResource() = %#v, %v", resource, err)
			}
			got, err := resource.Read(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if test.binary {
				data, ok := got.(*message.DataContent)
				if !ok {
					t.Fatalf("Read() = %T, want binary content", got)
				}
				value, err := data.Bytes()
				if err != nil || len(value) != 0 || data.MediaType != "application/octet-stream" {
					t.Fatalf("empty binary = %x, %q, %v", value, data.MediaType, err)
				}
			} else if test.text != "" {
				if got != test.text {
					t.Fatalf("Read() = %#v, want %q", got, test.text)
				}
			} else if got != nil {
				t.Fatalf("empty Read() = %#v, want nil", got)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if _, err := resource.Read(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled prefetched Read() error = %v", err)
			}
		})
	}
}

func TestSource_ResourceRootURI_HandlesInstructionLocations(t *testing.T) {
	for _, uri := range []string{
		"skill://unit-converter/SKILL.md",
		"skill://unit-converter/",
		"skill://unit-converter",
	} {
		t.Run(uri, func(t *testing.T) {
			server := newTestServer(t)
			server.text(indexURI, strings.ReplaceAll(skillIndex, skillURI, uri))
			server.text("skill://unit-converter/reference.md", "sibling")
			skill := loadOne(t, newSource(t, server, mcpskills.Config{}))
			resource, err := skill.GetResource(t.Context(), "reference.md")
			if err != nil || resource == nil {
				t.Fatalf("GetResource() = %#v, %v", resource, err)
			}
			if got, err := resource.Read(t.Context()); err != nil || got != "sibling" {
				t.Fatalf("Read() = %#v, %v", got, err)
			}
		})
	}
}

func TestSource_Cancellation_PropagatesAndDoesNotCache(t *testing.T) {
	for _, operation := range []string{"index", "content", "resource"} {
		t.Run(operation, func(t *testing.T) {
			server := newTestServer(t)
			server.text(indexURI, skillIndex)
			server.text(skillURI, skillMD)
			server.text("skill://unit-converter/reference.md", "reference")
			source := newSource(t, server, mcpskills.Config{})
			var skill *skills.Skill
			if operation != "index" {
				skill = loadOne(t, source)
			}
			started := make(chan struct{})
			var first atomic.Bool
			server.hook(func(ctx context.Context, uri string) error {
				if first.CompareAndSwap(false, true) {
					close(started)
					<-ctx.Done()
					return ctx.Err()
				}
				return nil
			})
			ctx, cancel := context.WithCancel(t.Context())
			t.Cleanup(cancel)
			done := make(chan error, 1)
			go func() {
				var err error
				switch operation {
				case "index":
					_, err = source.Skills(ctx, skills.SourceContext{})
				case "content":
					_, err = skill.GetContent(ctx)
				case "resource":
					_, err = skill.GetResource(ctx, "reference.md")
				}
				done <- err
			}()
			<-started
			cancel()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled %s error = %v", operation, err)
			}
			switch operation {
			case "index":
				_ = loadOne(t, source)
			case "content":
				if got := content(t, skill); got != skillMD {
					t.Fatalf("retried GetContent() = %q", got)
				}
			case "resource":
				resource, err := skill.GetResource(t.Context(), "reference.md")
				if err != nil || resource == nil {
					t.Fatalf("retried GetResource() = %#v, %v", resource, err)
				}
			}
		})
	}
}

func TestSource_ContentWaiter_CanCancel(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, skillIndex)
	server.text(skillURI, skillMD)
	skill := loadOne(t, newSource(t, server, mcpskills.Config{}))
	started, release := make(chan struct{}), make(chan struct{})
	server.hook(func(ctx context.Context, uri string) error {
		if uri == skillURI {
			close(started)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	first := make(chan error, 1)
	go func() {
		_, err := skill.GetContent(t.Context())
		first <- err
	}()
	<-started
	ctx, cancel := context.WithCancel(t.Context())
	second := make(chan error, 1)
	go func() {
		_, err := skill.GetContent(ctx)
		second <- err
	}()
	cancel()
	if err := <-second; !errors.Is(err, context.Canceled) {
		t.Fatalf("waiting GetContent() error = %v", err)
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestSource_Close_PreservesBorrowedSessionAndFetchedResources(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, skillIndex)
	server.text("skill://unit-converter/reference.md", "prefetched")
	source := newSource(t, server, mcpskills.Config{})
	skill := loadOne(t, source)
	resource, err := skill.GetResource(t.Context(), "reference.md")
	if err != nil || resource == nil {
		t.Fatalf("GetResource() = %#v, %v", resource, err)
	}
	for range 2 {
		if err := source.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := source.Skills(t.Context(), skills.SourceContext{}); !errors.Is(err, fs.ErrClosed) {
		t.Fatalf("Skills() after Close() error = %v", err)
	}
	if _, err := skill.GetContent(t.Context()); !errors.Is(err, fs.ErrClosed) {
		t.Fatalf("GetContent() after Close() error = %v", err)
	}
	if _, err := skill.GetResource(t.Context(), "reference.md"); !errors.Is(err, fs.ErrClosed) {
		t.Fatalf("GetResource() after Close() error = %v", err)
	}
	if got, err := resource.Read(t.Context()); err != nil || got != "prefetched" {
		t.Fatalf("prefetched Read() after Close() = %#v, %v", got, err)
	}
	if _, err := server.session.ReadResource(t.Context(), &mcp.ReadResourceParams{URI: indexURI}); err != nil {
		t.Fatalf("borrowed session was closed: %v", err)
	}
}

func TestSource_Provider_UsesLazyResourcesWithCachedDiscovery(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, skillIndex)
	server.text(skillURI, skillMD)
	server.text("skill://unit-converter/reference.md", "first")
	source := newSource(t, server, mcpskills.Config{})
	provider := skills.NewContextProvider(skills.ContextProviderOptions{
		Sources:     []skills.Source{source},
		OwnsSources: true,
	})
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, want := range []string{"first", "second"} {
		server.text("skill://unit-converter/reference.md", want)
		_, options, err := provider.Invoking(t.Context(), agent.InvokingContext{})
		if err != nil {
			t.Fatal(err)
		}
		instructions, _ := agent.GetOption(options, agent.WithInstructions)
		if !strings.Contains(instructions, "unit-converter") {
			t.Fatalf("instructions = %q", instructions)
		}
		var read tool.FuncTool
		for value := range agent.AllOptions(options, agent.WithTool) {
			if value.Name() == "read_skill_resource" {
				var ok bool
				read, ok = value.(tool.FuncTool)
				if !ok {
					t.Fatalf("resource tool type = %T", value)
				}
			}
		}
		if read == nil {
			t.Fatal("read_skill_resource tool not provided")
		}
		approval, ok := read.(tool.ApprovalRequiredTool)
		if !ok || !approval.ApprovalRequired() {
			t.Fatal("dynamic resource reads bypassed approval")
		}
		got, err := read.Call(t.Context(), `{"skillName":"unit-converter","resourceName":"reference.md"}`)
		if err != nil || got != want {
			t.Fatalf("read_skill_resource() = %#v, %v, want %q", got, err, want)
		}
	}
	if got := server.count(indexURI); got != 1 {
		t.Fatalf("index reads = %d, want cached discovery", got)
	}
	if got := server.count("skill://unit-converter/reference.md"); got != 2 {
		t.Fatalf("resource reads = %d, want fresh named lookups", got)
	}
	if err := provider.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Skills(t.Context(), skills.SourceContext{}); !errors.Is(err, fs.ErrClosed) {
		t.Fatalf("owned source remained open: %v", err)
	}
}

func TestSource_FeatureUsage(t *testing.T) {
	const childEnv = "AGENT_FRAMEWORK_MCP_SKILLS_FEATURE_USAGE_TEST"
	if os.Getenv(childEnv) != t.Name() {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command(exe, "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
		command.Env = append(os.Environ(),
			childEnv+"="+t.Name(),
			"AGENT_FRAMEWORK_FEATURE_MASK_DISABLED=",
			"AGENT_FRAMEWORK_USER_AGENT_DISABLED=",
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated feature-usage test: %v\n%s", err, output)
		}
		return
	}
	server := newTestServer(t)
	source := newSource(t, server, mcpskills.Config{})
	if got := telemetry.ApplyToUserAgent("", true); got != "" {
		t.Fatalf("construction marked feature usage: %q", got)
	}
	_ = loadSkills(t, source)
	if got := telemetry.ApplyToUserAgent("", true); got != "(feat=v1.80000)" {
		t.Fatalf("feature token = %q, want MCP Skills index 19 only", got)
	}
}

func TestNewSource_InvalidConfig_Panics(t *testing.T) {
	server := newTestServer(t)
	for name, configure := range map[string]func(*mcpskills.Config){
		"nil session":       func(config *mcpskills.Config) { config.Session = nil },
		"search depth":      func(config *mcpskills.Config) { config.ArchiveResourceSearchDepth = new(0) },
		"file count":        func(config *mcpskills.Config) { config.ArchiveMaxFileCount = new(-1) },
		"download size":     func(config *mcpskills.Config) { config.ArchiveMaxSizeBytes = new(int64(-1)) },
		"uncompressed size": func(config *mcpskills.Config) { config.ArchiveMaxUncompressedSizeBytes = new(int64(-1)) },
	} {
		t.Run(name, func(t *testing.T) {
			config := mcpskills.Config{Session: server.session}
			configure(&config)
			defer func() {
				if recover() == nil {
					t.Fatal("NewSource() did not panic")
				}
			}()
			mcpskills.NewSource(config)
		})
	}
}

func TestSource_GetResource_SiblingText_ReturnsContent(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, skillIndex)
	server.text(skillURI, skillMD)
	server.text("skill://unit-converter/references/checklist.md", "- check thing 1\n- check thing 2")
	skill := loadOne(t, newSource(t, server, mcpskills.Config{}))

	resource, err := skill.GetResource(t.Context(), "references/checklist.md")
	if err != nil || resource == nil {
		t.Fatalf("GetResource() = %#v, %v", resource, err)
	}
	got, err := resource.Read(t.Context())
	if err != nil || got != "- check thing 1\n- check thing 2" {
		t.Fatalf("Read() = %#v, %v", got, err)
	}
}

func TestSource_GetResource_SiblingBinary_ReturnsDataContent(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, skillIndex)
	server.text(skillURI, skillMD)
	server.blob("skill://unit-converter/assets/icon.bin", []byte{1, 2, 3, 4}, "application/octet-stream")
	skill := loadOne(t, newSource(t, server, mcpskills.Config{}))

	resource, err := skill.GetResource(t.Context(), "assets/icon.bin")
	if err != nil || resource == nil {
		t.Fatalf("GetResource() = %#v, %v", resource, err)
	}
	got, err := resource.Read(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	data, ok := got.(*message.DataContent)
	if !ok {
		t.Fatalf("Read() type = %T, want *message.DataContent", got)
	}
	value, err := data.Bytes()
	if err != nil || !bytes.Equal(value, []byte{1, 2, 3, 4}) || data.MediaType != "application/octet-stream" {
		t.Fatalf("binary content = %x, %q, %v", value, data.MediaType, err)
	}
}

func TestSource_GetResource_UnknownName_ReturnsNil(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, skillIndex)
	server.text(skillURI, skillMD)
	skill := loadOne(t, newSource(t, server, mcpskills.Config{}))

	resource, err := skill.GetResource(t.Context(), "references/does-not-exist.md")
	if err != nil || resource != nil {
		t.Fatalf("GetResource() = %#v, %v, want not found", resource, err)
	}
}

func TestSource_GetResource_PathTraversalName_ReturnsNil(t *testing.T) {
	for _, name := range []string{"../escape.md", "references/../../escape.md", ".."} {
		t.Run(name, func(t *testing.T) {
			server := newTestServer(t)
			server.text(indexURI, skillIndex)
			server.text(skillURI, skillMD)
			skill := loadOne(t, newSource(t, server, mcpskills.Config{}))

			resource, err := skill.GetResource(t.Context(), name)
			if err != nil || resource != nil {
				t.Fatalf("GetResource(%q) = %#v, %v, want not found", name, resource, err)
			}
		})
	}
}

func TestSource_IndexEntryWithInvalidName_IsSkipped(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, `{"$schema":"https://schemas.agentskills.io/discovery/0.2.0/schema.json","skills":[
{"name":"UnitConverter","type":"skill-md","description":"Convert between common units.","url":"skill://UnitConverter/SKILL.md"}]}`)
	source := newSource(t, server, mcpskills.Config{})

	if got := loadSkills(t, source); len(got) != 0 {
		t.Fatalf("invalid name returned %d skills", len(got))
	}
}

func TestSource_IndexEntryWithMissingRequiredFields_IsSkipped(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, `{"$schema":"https://schemas.agentskills.io/discovery/0.2.0/schema.json","skills":[
{"name":"unit-converter","type":"skill-md"}]}`)
	source := newSource(t, server, mcpskills.Config{})

	if got := loadSkills(t, source); len(got) != 0 {
		t.Fatalf("missing description and URL returned %d skills", len(got))
	}
}

func TestSource_ArchiveEntryWithUnreadableResource_IsSkipped(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, `{"$schema":"https://schemas.agentskills.io/discovery/0.2.0/schema.json","skills":[
{"name":"some-skill","type":"archive","description":"Packaged skill.","url":"skill://some-skill.tar.gz"}]}`)
	source := newSource(t, server, mcpskills.Config{})

	if got := loadSkills(t, source); len(got) != 0 {
		t.Fatalf("unreadable archive returned %d skills", len(got))
	}
}

func TestSource_IndexEntryWithTemplateType_IsSkipped(t *testing.T) {
	server := newTestServer(t)
	server.text(indexURI, `{"$schema":"https://schemas.agentskills.io/discovery/0.2.0/schema.json","skills":[
{"type":"mcp-resource-template","description":"Per-product documentation skill","url":"skill://docs/{product}/SKILL.md"}]}`)
	source := newSource(t, server, mcpskills.Config{})

	if got := loadSkills(t, source); len(got) != 0 {
		t.Fatalf("resource template returned %d skills", len(got))
	}
}
