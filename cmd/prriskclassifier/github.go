// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"slices"
	"strings"
)

type commandRunner func(ctx context.Context, args ...string) (string, error)

func execGH(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return stdout.String(), fmt.Errorf("gh %s: %s", strings.Join(args, " "), message)
	}
	return stdout.String(), nil
}

type riskLabel struct {
	Name        string
	Color       string
	Description string
}

var managedLabels = []riskLabel{
	{Name: riskLow, Color: "0E8A16", Description: "Limited blast radius and straightforward rollback"},
	{Name: riskMedium, Color: "FBCA04", Description: "Contained production impact requiring normal review depth"},
	{Name: riskHigh, Color: "B60205", Description: "Large blast radius, difficult rollback, or sensitive behavior"},
	{Name: pendingAutoRisk, Color: "BFDADC", Description: "Automatic risk classification is in progress"},
	{Name: failedAutoRisk, Color: "6E7781", Description: "Automatic risk classification was inconclusive or failed"},
}

type riskClient interface {
	ensureLabels(context.Context) error
	pullRequestSignals(context.Context, int) ([]string, []string, error)
	pullRequestLabels(context.Context, int) ([]string, error)
	addLabel(context.Context, int, string) error
	removeLabel(context.Context, int, string) error
}

type ghClient struct {
	repo string
	run  commandRunner
}

func (c *ghClient) withRepo(args ...string) []string {
	return append([]string{"--repo", c.repo}, args...)
}

func (c *ghClient) ensureLabels(ctx context.Context) error {
	endpoint := fmt.Sprintf("repos/%s/labels?per_page=100", c.repo)
	out, err := c.run(ctx, "api", "--paginate", "--slurp", endpoint)
	if err != nil {
		return err
	}
	var pages [][]struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(out), &pages); err != nil {
		return fmt.Errorf("parse repository labels: %w", err)
	}
	var existing []struct {
		Name string `json:"name"`
	}
	for _, page := range pages {
		existing = append(existing, page...)
	}
	for _, label := range managedLabels {
		if slices.ContainsFunc(existing, func(item struct {
			Name string `json:"name"`
		},
		) bool {
			return item.Name == label.Name
		}) {
			continue
		}
		if _, err := c.run(ctx, c.withRepo(
			"label", "create", label.Name,
			"--color", label.Color,
			"--description", label.Description,
		)...); err != nil {
			return fmt.Errorf("create label %s: %w", label.Name, err)
		}
	}
	return nil
}

func (c *ghClient) pullRequestSignals(ctx context.Context, number int) ([]string, []string, error) {
	labels, err := c.pullRequestLabels(ctx, number)
	if err != nil {
		return nil, nil, err
	}

	endpoint := fmt.Sprintf("repos/%s/pulls/%d/files?per_page=100", c.repo, number)
	out, err := c.run(ctx, "api", "--paginate", "--slurp", endpoint)
	if err != nil {
		return nil, nil, err
	}
	var pages [][]struct {
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal([]byte(out), &pages); err != nil {
		return nil, nil, fmt.Errorf("parse pull request files: %w", err)
	}
	var files []string
	for _, page := range pages {
		for _, file := range page {
			files = append(files, file.Filename)
		}
	}
	return files, labels, nil
}

func (c *ghClient) pullRequestLabels(ctx context.Context, number int) ([]string, error) {
	endpoint := fmt.Sprintf("repos/%s/issues/%d", c.repo, number)
	out, err := c.run(ctx, "api", endpoint)
	if err != nil {
		return nil, err
	}
	var view struct {
		Labels []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal([]byte(out), &view); err != nil {
		return nil, fmt.Errorf("parse pull request labels: %w", err)
	}
	labels := make([]string, 0, len(view.Labels))
	for _, label := range view.Labels {
		labels = append(labels, label.Name)
	}
	return labels, nil
}

func (c *ghClient) addLabel(ctx context.Context, number int, label string) error {
	endpoint := fmt.Sprintf("repos/%s/issues/%d/labels", c.repo, number)
	_, err := c.run(ctx, "api", "--method", "POST", endpoint, "-f", "labels[]="+label)
	return err
}

func (c *ghClient) removeLabel(ctx context.Context, number int, label string) error {
	endpoint := fmt.Sprintf("repos/%s/issues/%d/labels/%s", c.repo, number, url.PathEscape(label))
	_, err := c.run(ctx, "api", "--method", "DELETE", endpoint)
	return err
}
