// Copyright (c) Microsoft. All rights reserved.

package main

import (
	"slices"
	"strings"
)

func allExamples() []ExampleDefinition {
	var examples []ExampleDefinition
	for _, category := range availableCategories() {
		examples = append(examples, exampleSets[category]...)
	}
	return examples
}

func lookupExampleSet(category string) ([]ExampleDefinition, bool) {
	for name, examples := range exampleSets {
		if strings.EqualFold(name, category) {
			return examples, true
		}
	}
	return nil, false
}

func availableCategories() []string {
	categories := make([]string, 0, len(exampleSets))
	for category := range exampleSets {
		categories = append(categories, category)
	}
	slices.Sort(categories)
	return categories
}

func inputLines(values ...string) []*string {
	inputs := make([]*string, 0, len(values))
	for _, value := range values {
		value := value
		inputs = append(inputs, &value)
	}
	return inputs
}
