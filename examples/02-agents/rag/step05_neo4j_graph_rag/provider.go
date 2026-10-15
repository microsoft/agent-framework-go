// Copyright (c) Microsoft. All rights reserved.
// Portions copyright (c) 2025 Ryan Knight. See LICENSE.neo4j.

package main

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf16"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/message"
	"github.com/microsoft/agent-framework-go/message/messagefilter"
	"github.com/neo4j/neo4j-go-driver/v5/neo4j"
)

const contextPrompt = "Use the retrieved Neo4j graph context to answer accurately and call out when context is missing."

type graphQuery func(context.Context, string, map[string]any) ([]*neo4j.Record, error)

func newGraphContextProvider(query graphQuery, index string) agent.ContextProvider {
	return agent.NewContextProvider(agent.ContextProviderConfig{
		SourceID:                  "Neo4jContextProvider",
		ProvideInputMessageFilter: messagefilter.PassThrough,
		Provide: func(ctx context.Context, invoking agent.InvokingContext) ([]*message.Message, []agent.Option, error) {
			var recent []string
			for _, msg := range invoking.Messages {
				if msg == nil || msg.Role != message.RoleUser && msg.Role != message.RoleAssistant {
					continue
				}
				text := msg.String()
				if strings.TrimSpace(text) != "" {
					recent = append(recent, text)
				}
			}
			recent = recent[max(0, len(recent)-10):]
			searchText := extractKeywords(strings.Join(recent, "\n"))
			if strings.TrimSpace(searchText) == "" {
				return nil, nil, nil
			}
			cypher := `CALL db.index.fulltext.queryNodes($index_name, $query)
YIELD node, score
WITH node, score
ORDER BY score DESC
LIMIT $top_k
` + retrievalQuery + `
LIMIT $top_k`
			records, err := query(ctx, cypher, map[string]any{
				"index_name": index, "query": searchText, "top_k": 5,
			})
			if err != nil {
				return nil, nil, err
			}
			if len(records) == 0 {
				return nil, nil, nil
			}
			messages := []*message.Message{message.NewText(contextPrompt)}
			for _, record := range records {
				text, err := formatRecord(record)
				if err != nil {
					return nil, nil, err
				}
				if text != "" {
					messages = append(messages, message.NewText(text))
				}
			}
			return messages, nil, nil
		},
	})
}

var wordPattern = regexp.MustCompile(`[\p{L}\p{M}\p{Nd}\p{Pc}]+`)

var stopWords = strings.Fields(`what where who when why how which whose
is are was were be been being have has had do does did
will would could should can may might must
a an the of in on at to for with by from as into about
i me my we us our you your it its they them their
and or but if then so that this these those
tell show find give get list describe explain any all some
most more less involve related regarding concerning`)

func extractKeywords(text string) string {
	var keywords []string
	for _, word := range wordPattern.FindAllString(strings.ToLower(text), -1) {
		if len(utf16.Encode([]rune(word))) > 1 && !slices.Contains(stopWords, word) {
			keywords = append(keywords, word)
		}
	}
	return strings.Join(keywords, " ")
}

func formatRecord(record *neo4j.Record) (string, error) {
	if record == nil {
		return "", fmt.Errorf("Neo4j returned a nil record")
	}
	content := ""
	if value, ok := record.Get("text"); ok && value != nil {
		content = fmt.Sprint(value)
	} else {
		foundString := false
		for _, value := range record.Values {
			if text, ok := value.(string); ok {
				content = text
				foundString = true
				break
			}
		}
		if !foundString {
			for _, value := range record.Values {
				if value != nil {
					content = fmt.Sprint(value)
					break
				}
			}
		}
	}
	var parts []string
	if score, ok := record.Get("score"); ok && score != nil {
		value, ok := score.(float64)
		if !ok {
			return "", fmt.Errorf("Neo4j score must be a number, got %T", score)
		}
		parts = append(parts, fmt.Sprintf("[Score: %.3f]", value))
	}
	for i, key := range record.Keys {
		value := record.Values[i]
		if key == "text" || key == "score" || value == nil {
			continue
		}
		var field string
		switch value := value.(type) {
		case []any:
			items := make([]string, len(value))
			for j, item := range value {
				if item != nil {
					items[j] = fmt.Sprint(item)
				}
			}
			if len(items) > 0 {
				field = strings.Join(items, ", ")
			} else {
				continue
			}
		default:
			field = fmt.Sprint(value)
		}
		parts = append(parts, fmt.Sprintf("[%s: %s]", key, field))
	}
	if content != "" {
		parts = append(parts, content)
	}
	return strings.Join(parts, " "), nil
}
