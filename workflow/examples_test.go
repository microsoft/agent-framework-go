// Copyright (c) Microsoft. All rights reserved.

package workflow_test

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/microsoft/agent-framework-go/workflow"
)

func ExampleNewExecutor_function() {
	executor := workflow.NewExecutor("length", func(input string) int {
		return len(input)
	})

	var sent any
	var yielded any
	result, err := executor.Execute(exampleContext(&sent, &yielded), "hello")
	if err != nil {
		panic(err)
	}

	fmt.Println(result)
	fmt.Println(sent)
	fmt.Println(yielded)

	// Output:
	// 5
	// 5
	// 5
}

func ExampleNewExecutor_context() {
	executor := workflow.NewExecutor("normalize", func(ctx *workflow.Context, input string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return strings.ToLower(input), nil
	})

	result, err := executor.Execute(exampleContext(nil, nil), "HELLO")
	if err != nil {
		panic(err)
	}

	fmt.Println(result)

	// Output:
	// hello
}

func ExampleNewExecutor_struct() {
	executor := workflow.NewExecutor("reviewer", exampleReviewer{})
	descriptor := executor.DescribeProtocol()

	fmt.Println(slices.Contains(descriptor.Accepts, reflect.TypeFor[exampleDraft]()))
	fmt.Println(slices.Contains(descriptor.Sends, reflect.TypeFor[exampleReviewRequest]()))
	fmt.Println(slices.Contains(descriptor.Yields, reflect.TypeFor[exampleReviewReport]()))

	// Output:
	// true
	// true
	// true
}

type exampleDraft struct {
	Text string
}

type exampleReviewRequest struct {
	Text string
}

type exampleReviewReport struct {
	Approved bool
}

type exampleReviewer struct {
	_ workflow.AttrSendsMessage[exampleReviewRequest]
	_ workflow.AttrYieldsOutput[exampleReviewReport]
}

func (exampleReviewer) Handle(ctx *workflow.Context, draft exampleDraft) error {
	return ctx.SendMessage("", exampleReviewRequest{Text: strings.TrimSpace(draft.Text)})
}

func exampleContext(sent *any, yielded *any) *workflow.Context {
	return &workflow.Context{
		Context: context.Background(),
		AddEvent: func(workflow.Event) error {
			return nil
		},
		SendMessage: func(_ string, message any) error {
			if sent != nil {
				*sent = message
			}
			return nil
		},
		YieldOutput: func(output any) error {
			if yielded != nil {
				*yielded = output
			}
			return nil
		},
	}
}
