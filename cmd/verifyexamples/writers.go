// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"encoding/csv"
	"fmt"
	"html"
	"os"
	"strings"
	"sync"
	"time"
)

type LogFileWriter struct {
	path string
	lock sync.Mutex
}

func NewLogFileWriter(path string) *LogFileWriter {
	return &LogFileWriter{path: path}
}

func (w *LogFileWriter) WriteHeader() error {
	content := fmt.Sprintf("Example Verification Log — %s UTC\n%s\n\n", time.Now().UTC().Format("2006-01-02 15:04:05"), strings.Repeat("═", 72))
	return os.WriteFile(w.path, []byte(content), 0o666)
}

func (w *LogFileWriter) WriteSkipped(name string, reason string) error {
	return w.append(fmt.Sprintf("── %s ──\nStatus: SKIPPED — %s\n\n", name, reason))
}

func (w *LogFileWriter) WriteExampleResult(result VerificationResult) error {
	var sb strings.Builder
	sb.WriteString(strings.Repeat("─", 72))
	sb.WriteByte('\n')
	fmt.Fprintf(&sb, "── %s ──\n", result.ExampleName)
	if result.Passed {
		sb.WriteString("Status: PASSED\n\n")
	} else {
		sb.WriteString("Status: FAILED\n\n")
	}
	for _, line := range result.LogLines {
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')
	if strings.TrimSpace(result.Stdout) != "" {
		sb.WriteString("--- stdout ---\n")
		sb.WriteString(strings.TrimRight(result.Stdout, "\r\n"))
		sb.WriteString("\n--- end stdout ---\n\n")
	}
	if strings.TrimSpace(result.Stderr) != "" {
		sb.WriteString("--- stderr ---\n")
		sb.WriteString(strings.TrimRight(result.Stderr, "\r\n"))
		sb.WriteString("\n--- end stderr ---\n\n")
	}
	if len(result.Failures) > 0 {
		sb.WriteString("Failures:\n")
		for _, failure := range result.Failures {
			fmt.Fprintf(&sb, "  ✗ %s\n", failure)
		}
		sb.WriteByte('\n')
	}
	if result.AIReasoning != "" {
		sb.WriteString("AI Reasoning:\n")
		sb.WriteString(result.AIReasoning)
		sb.WriteString("\n\n")
	}
	return w.append(sb.String())
}

func (w *LogFileWriter) WriteSummary(orderedResults []VerificationResult, skipped []SkippedExample, elapsed time.Duration) error {
	passCount := 0
	failCount := 0
	for _, result := range orderedResults {
		if result.Passed {
			passCount++
		} else {
			failCount++
		}
	}
	var sb strings.Builder
	sb.WriteString(strings.Repeat("═", 72))
	sb.WriteString("\nSUMMARY\n\n")
	for _, result := range orderedResults {
		marker := "✗"
		if result.Passed {
			marker = "✓"
		}
		fmt.Fprintf(&sb, "  %s %s: %s\n", marker, result.ExampleName, result.Summary)
	}
	for _, skippedExample := range skipped {
		fmt.Fprintf(&sb, "  ○ %s: Skipped — %s\n", skippedExample.Name, skippedExample.Reason)
	}
	fmt.Fprintf(&sb, "\nResults: %d passed", passCount)
	if failCount > 0 {
		fmt.Fprintf(&sb, ", %d failed", failCount)
	}
	if len(skipped) > 0 {
		fmt.Fprintf(&sb, ", %d skipped", len(skipped))
	}
	fmt.Fprintf(&sb, "\nElapsed: %02d:%02d:%02d\n", int(elapsed.Hours()), int(elapsed.Minutes())%60, int(elapsed.Seconds())%60)
	return w.append(sb.String())
}

func (w *LogFileWriter) append(text string) error {
	w.lock.Lock()
	defer w.lock.Unlock()
	file, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()
	_, err = file.WriteString(text)
	return err
}

func writeCSV(path string, orderedResults []VerificationResult, skipped []SkippedExample, examples []ExampleDefinition) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()

	pathLookup := map[string]string{}
	for _, example := range examples {
		pathLookup[example.Name] = example.ProjectPath
	}
	w := csv.NewWriter(file)
	defer w.Flush()
	if err := w.Write([]string{"Example", "ProjectPath", "Status", "FailedChecks", "Failures"}); err != nil {
		return err
	}
	for _, result := range orderedResults {
		status := "FAILED"
		if result.Passed {
			status = "PASSED"
		}
		if err := w.Write([]string{result.ExampleName, pathLookup[result.ExampleName], status, fmt.Sprint(len(result.Failures)), strings.Join(result.Failures, "; ")}); err != nil {
			return err
		}
	}
	for _, skippedExample := range skipped {
		if err := w.Write([]string{skippedExample.Name, pathLookup[skippedExample.Name], "SKIPPED", "0", skippedExample.Reason}); err != nil {
			return err
		}
	}
	return w.Error()
}

func writeMarkdown(path string, orderedResults []VerificationResult, skipped []SkippedExample, elapsed time.Duration) error {
	passCount := 0
	failCount := 0
	for _, result := range orderedResults {
		if result.Passed {
			passCount++
		} else {
			failCount++
		}
	}
	var sb strings.Builder
	sb.WriteString("# Example Verification Results\n\n")
	fmt.Fprintf(&sb, "**%d passed, %d failed, %d skipped** | Elapsed: %02d:%02d:%02d\n\n", passCount, failCount, len(skipped), int(elapsed.Hours()), int(elapsed.Minutes())%60, int(elapsed.Seconds())%60)
	sb.WriteString("| Example | Status | Failed Checks | Failures |\n")
	sb.WriteString("|--------|--------|---------------|----------|\n")
	for _, result := range orderedResults {
		status := "❌ FAILED"
		if result.Passed {
			status = "✅ PASSED"
		}
		fmt.Fprintf(&sb, "| %s | %s | %d | %s |\n", mdEscape(result.ExampleName), status, len(result.Failures), mdEscape(strings.Join(result.Failures, "; ")))
	}
	for _, skippedExample := range skipped {
		fmt.Fprintf(&sb, "| %s | ⏭️ SKIPPED | 0 | %s |\n", mdEscape(skippedExample.Name), mdEscape(skippedExample.Reason))
	}

	var failuresWithReasoning []VerificationResult
	for _, result := range orderedResults {
		if !result.Passed && result.AIReasoning != "" {
			failuresWithReasoning = append(failuresWithReasoning, result)
		}
	}
	if len(failuresWithReasoning) > 0 {
		sb.WriteString("\n## Failure Details\n\n")
		for _, result := range failuresWithReasoning {
			fmt.Fprintf(&sb, "<details><summary><strong>%s</strong></summary>\n\n", html.EscapeString(result.ExampleName))
			for _, failure := range result.Failures {
				fmt.Fprintf(&sb, "- %s\n", mdEscape(failure))
			}
			sb.WriteString("\n**AI Reasoning:**\n\n```\n")
			sb.WriteString(result.AIReasoning)
			sb.WriteString("\n```\n\n</details>\n\n")
		}
	}
	return os.WriteFile(path, []byte(sb.String()), 0o666)
}

func mdEscape(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(value, "|", "\\|"), "\n", " "), "\r", "")
}
