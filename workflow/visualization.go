// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// ToMermaidString renders wf as a Mermaid flowchart definition.
//
// It mirrors .NET's WorkflowVisualizer.ToMermaidString: nodes are emitted for
// every bound executor (the start executor is highlighted), and edges are drawn
// from the reflected edge metadata. Conditional edges are dashed, edge labels
// are preserved, fan-out edges expand to one edge per target, and fan-in edges
// (more than one source) are drawn through a synthesized junction node. Nested
// sub-workflows (bindings whose RawValue is a *Workflow) are rendered as nested
// subgraphs.
func ToMermaidString(wf *Workflow) string {
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	b.WriteString("    classDef startNode fill:#2E7D32,stroke:#1B5E20,color:#ffffff;\n")
	if wf != nil {
		writeMermaidWorkflow(&b, wf, "", 1, map[*Workflow]bool{})
	}
	return b.String()
}

// ToDotString renders wf as a Graphviz DOT digraph definition.
//
// It mirrors .NET's WorkflowVisualizer.ToDotString using the same graph shape
// as [ToMermaidString]: the start executor is filled, conditional edges are
// dashed, edge labels are preserved, fan-in edges route through a junction
// node, and nested sub-workflows are emitted as DOT clusters.
func ToDotString(wf *Workflow) string {
	var b strings.Builder
	b.WriteString("digraph Workflow {\n")
	b.WriteString("    rankdir=TB;\n")
	b.WriteString("    node [shape=box];\n")
	if wf != nil {
		writeDotWorkflow(&b, wf, "", 1, map[*Workflow]bool{})
	}
	b.WriteString("}\n")
	return b.String()
}

func writeMermaidWorkflow(b *strings.Builder, wf *Workflow, prefix string, depth int, visited map[*Workflow]bool) {
	if visited[wf] {
		return
	}
	visited[wf] = true
	indent := strings.Repeat("    ", depth)
	for _, id := range sortedExecutorIDs(wf) {
		binding := wf.executorBindings[id]
		nodeID := mermaidID(prefix + id)
		if sub, ok := binding.RawValue.(*Workflow); ok && sub != nil {
			fmt.Fprintf(b, "%ssubgraph %s [\"%s\"]\n", indent, nodeID, mermaidLabel(id))
			writeMermaidWorkflow(b, sub, prefix+id+"/", depth+1, visited)
			fmt.Fprintf(b, "%send\n", indent)
			continue
		}
		fmt.Fprintf(b, "%s%s[\"%s\"]\n", indent, nodeID, mermaidLabel(id))
		if id == wf.startExecutorID {
			fmt.Fprintf(b, "%sclass %s startNode;\n", indent, nodeID)
		}
	}
	for _, info := range reflectUniqueEdges(wf) {
		writeMermaidEdge(b, indent, prefix, info)
	}
}

func writeMermaidEdge(b *strings.Builder, indent, prefix string, info EdgeInfo) {
	sources := info.Connection.SourceIDs
	sinks := info.Connection.SinkIDs
	arrow := "-->"
	if info.HasCondition {
		arrow = "-.->"
	}
	label := ""
	if info.Label != "" {
		label = "|" + mermaidLabel(info.Label) + "|"
	}
	if len(sources) > 1 {
		junction := mermaidID(prefix + fanInJunctionID(sources, sinks))
		fmt.Fprintf(b, "%s%s{{\"fan-in\"}}\n", indent, junction)
		for _, s := range sources {
			fmt.Fprintf(b, "%s%s --> %s\n", indent, mermaidID(prefix+s), junction)
		}
		for _, t := range sinks {
			fmt.Fprintf(b, "%s%s %s%s %s\n", indent, junction, arrow, label, mermaidID(prefix+t))
		}
		return
	}
	for _, s := range sources {
		for _, t := range sinks {
			fmt.Fprintf(b, "%s%s %s%s %s\n", indent, mermaidID(prefix+s), arrow, label, mermaidID(prefix+t))
		}
	}
}

func writeDotWorkflow(b *strings.Builder, wf *Workflow, prefix string, depth int, visited map[*Workflow]bool) {
	if visited[wf] {
		return
	}
	visited[wf] = true
	indent := strings.Repeat("    ", depth)
	for _, id := range sortedExecutorIDs(wf) {
		binding := wf.executorBindings[id]
		nodeID := prefix + id
		if sub, ok := binding.RawValue.(*Workflow); ok && sub != nil {
			fmt.Fprintf(b, "%ssubgraph \"cluster_%s\" {\n", indent, dotEscape(nodeID))
			fmt.Fprintf(b, "%s    label=\"%s\";\n", indent, dotEscape(id))
			writeDotWorkflow(b, sub, prefix+id+"/", depth+1, visited)
			fmt.Fprintf(b, "%s}\n", indent)
			continue
		}
		if id == wf.startExecutorID {
			fmt.Fprintf(b, "%s\"%s\" [label=\"%s\", style=filled, fillcolor=\"#2E7D32\", fontcolor=\"white\"];\n", indent, dotEscape(nodeID), dotEscape(id))
		} else {
			fmt.Fprintf(b, "%s\"%s\" [label=\"%s\"];\n", indent, dotEscape(nodeID), dotEscape(id))
		}
	}
	for _, info := range reflectUniqueEdges(wf) {
		writeDotEdge(b, indent, prefix, info)
	}
}

func writeDotEdge(b *strings.Builder, indent, prefix string, info EdgeInfo) {
	sources := info.Connection.SourceIDs
	sinks := info.Connection.SinkIDs
	var attrParts []string
	if info.Label != "" {
		attrParts = append(attrParts, fmt.Sprintf("label=\"%s\"", dotEscape(info.Label)))
	}
	if info.HasCondition {
		attrParts = append(attrParts, "style=dashed")
	}
	attrs := ""
	if len(attrParts) > 0 {
		attrs = " [" + strings.Join(attrParts, ", ") + "]"
	}
	if len(sources) > 1 {
		junction := prefix + fanInJunctionID(sources, sinks)
		fmt.Fprintf(b, "%s\"%s\" [shape=diamond, label=\"fan-in\"];\n", indent, dotEscape(junction))
		for _, s := range sources {
			fmt.Fprintf(b, "%s\"%s\" -> \"%s\";\n", indent, dotEscape(prefix+s), dotEscape(junction))
		}
		for _, t := range sinks {
			fmt.Fprintf(b, "%s\"%s\" -> \"%s\"%s;\n", indent, dotEscape(junction), dotEscape(prefix+t), attrs)
		}
		return
	}
	for _, s := range sources {
		for _, t := range sinks {
			fmt.Fprintf(b, "%s\"%s\" -> \"%s\"%s;\n", indent, dotEscape(prefix+s), dotEscape(prefix+t), attrs)
		}
	}
}

func sortedExecutorIDs(wf *Workflow) []string {
	ids := make([]string, 0, len(wf.executorBindings))
	for id := range wf.executorBindings {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// reflectUniqueEdges returns the workflow's edges deduplicated by connection and
// metadata, in a stable order. Fan-in edges are registered under every source in
// [Workflow.ReflectEdges], so deduplication avoids emitting them multiple times.
func reflectUniqueEdges(wf *Workflow) []EdgeInfo {
	seen := map[string]bool{}
	var out []EdgeInfo
	for _, list := range wf.ReflectEdges() {
		for _, info := range list {
			key := edgeSignature(info)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return edgeSignature(out[i]) < edgeSignature(out[j])
	})
	return out
}

func edgeSignature(info EdgeInfo) string {
	return strings.Join(info.Connection.SourceIDs, ",") + ">" +
		strings.Join(info.Connection.SinkIDs, ",") + "|" +
		info.Label + "|" + strconv.FormatBool(info.HasCondition)
}

func fanInJunctionID(sources, sinks []string) string {
	return "fanin_" + strings.Join(sources, "_") + "__" + strings.Join(sinks, "_")
}

func mermaidID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "n"
	}
	if out[0] >= '0' && out[0] <= '9' {
		return "n" + out
	}
	return out
}

func mermaidLabel(s string) string {
	return strings.ReplaceAll(s, "\"", "#quot;")
}

func dotEscape(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	return s
}
