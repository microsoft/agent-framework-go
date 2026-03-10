// Copyright (c) Microsoft. All rights reserved.

package messagefilter

import (
	"context"
	"errors"
	"testing"

	"github.com/microsoft/agent-framework-go/message"
)

func TestPassThrough(t *testing.T) {
	m1 := message.NewText("a")
	m2 := message.NewText("b")
	out, err := PassThrough(context.Background(), []*message.Message{m1, m2})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 || out[0] != m1 || out[1] != m2 {
		t.Fatal("expected pass-through behavior")
	}
}

func TestNone(t *testing.T) {
	out, err := None(context.Background(), []*message.Message{message.NewText("a")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected no messages, got %d", len(out))
	}
}

func TestExternalOnly(t *testing.T) {
	external := message.NewText("external")
	internal := message.NewText("internal")
	internal.SourceID = "provider"
	out, err := ExternalOnly(context.Background(), []*message.Message{external, internal})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 || out[0] != external {
		t.Fatal("expected only external messages")
	}
}

func TestSources(t *testing.T) {
	allowed := message.NewText("allowed")
	allowed.SourceID = "a"
	blocked := message.NewText("blocked")
	blocked.SourceID = "b"

	filter := Sources("a")
	out, err := filter(context.Background(), []*message.Message{allowed, blocked})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 || out[0] != allowed {
		t.Fatal("expected only allowed SourceID messages")
	}
}

func TestNotSources(t *testing.T) {
	keep := message.NewText("keep")
	keep.SourceID = "a"
	blocked := message.NewText("blocked")
	blocked.SourceID = "b"

	filter := NotSources("b")
	out, err := filter(context.Background(), []*message.Message{keep, blocked})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 || out[0] != keep {
		t.Fatal("expected messages not in deny list")
	}
}

func TestOr_ExternalOnlyOrSources(t *testing.T) {
	external := message.NewText("external")
	allowed := message.NewText("allowed")
	allowed.SourceID = "a"
	blocked := message.NewText("blocked")
	blocked.SourceID = "b"

	filter := Or(ExternalOnly, Sources("a"))
	out, err := filter(context.Background(), []*message.Message{external, allowed, blocked})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 || out[0] != external || out[1] != allowed {
		t.Fatal("expected external or allowed SourceID messages")
	}
}

func TestBuiltInFilters_DoNotAddMessages(t *testing.T) {
	m1 := message.NewText("a")
	m2 := message.NewText("b")
	m2.SourceID = "x"
	messages := []*message.Message{m1, m2}

	filters := []Filter{
		PassThrough,
		None,
		ExternalOnly,
		NotSources("x"),
		Or(ExternalOnly, Sources("x")),
	}

	for _, filter := range filters {
		out, err := filter(context.Background(), append([]*message.Message(nil), messages...))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for _, msg := range out {
			if msg != m1 && msg != m2 {
				t.Fatal("filter returned a message that was not in the input")
			}
		}
	}
}

func TestAnd_AppliesFiltersInOrder(t *testing.T) {
	m1 := message.NewText("external")
	m2 := message.NewText("allowed")
	m2.SourceID = "a"
	m3 := message.NewText("blocked")
	m3.SourceID = "b"

	filter := And(PassThrough, Or(ExternalOnly, Sources("a")))
	out, err := filter(context.Background(), []*message.Message{m1, m2, m3})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 || out[0] != m1 || out[1] != m2 {
		t.Fatal("expected chained filters to run in order")
	}
}

func TestAnd_SkipsNilFilters(t *testing.T) {
	m1 := message.NewText("external")
	filter := And(nil, PassThrough, nil)
	out, err := filter(context.Background(), []*message.Message{m1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 || out[0] != m1 {
		t.Fatal("expected nil filters to be ignored")
	}
}

func TestAnd_StopsOnError(t *testing.T) {
	expected := errors.New("boom")
	called := false
	filter := And(
		func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			return messages, expected
		},
		func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			called = true
			return messages, nil
		},
	)

	_, err := filter(context.Background(), []*message.Message{message.NewText("a")})
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
	if called {
		t.Fatal("expected chain to stop after first error")
	}
}

func TestOr_AppliesUnion(t *testing.T) {
	external := message.NewText("external")
	allowed := message.NewText("allowed")
	allowed.SourceID = "a"
	blocked := message.NewText("blocked")
	blocked.SourceID = "b"

	filter := Or(ExternalOnly, Sources("a"))
	out, err := filter(context.Background(), []*message.Message{external, allowed, blocked})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 2 || out[0] != external || out[1] != allowed {
		t.Fatal("expected union of filter outputs")
	}
}

func TestOr_SkipsNilFilters(t *testing.T) {
	external := message.NewText("external")
	filter := Or(nil, ExternalOnly, nil)
	out, err := filter(context.Background(), []*message.Message{external})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 1 || out[0] != external {
		t.Fatal("expected nil filters to be ignored")
	}
}

func TestOr_StopsOnError(t *testing.T) {
	expected := errors.New("boom")
	called := false
	filter := Or(
		func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			return nil, expected
		},
		func(_ context.Context, messages []*message.Message) ([]*message.Message, error) {
			called = true
			return messages, nil
		},
	)

	_, err := filter(context.Background(), []*message.Message{message.NewText("a")})
	if !errors.Is(err, expected) {
		t.Fatalf("expected %v, got %v", expected, err)
	}
	if called {
		t.Fatal("expected Or to stop after first error")
	}
}

func TestOr_WithoutFilters_ReturnsNone(t *testing.T) {
	out, err := Or()(context.Background(), []*message.Message{message.NewText("a")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out) != 0 {
		t.Fatal("expected no messages when no filters are provided")
	}
}
