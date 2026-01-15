// Copyright (c) Microsoft. All rights reserved.

package logger

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"time"

	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/internal/slogx"
	"github.com/microsoft/agent-framework-go/message"
)

type agentContextKey struct{}

type agentContext struct {
	ID   string
	Name string
}

func WithAgentContext(ctx context.Context, id, name string) context.Context {
	return context.WithValue(ctx, agentContextKey{}, agentContext{id, name})
}

type Config struct {
	Logger        *slog.Logger
	SensitiveData bool
}

func New(cfg Config) middleware.Middleware {
	return &logger{l: slogx.Logger{
		Logger:        cfg.Logger,
		SensitiveData: cfg.SensitiveData,
		Type:          slogx.TypeMiddleware,
		Name:          "logger",
	}}
}

type logger struct {
	l slogx.Logger
}

func (l *logger) Run(ctx context.Context, next middleware.RunFunc, messages []*message.Message, opts ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		start := time.Now()
		l.log(ctx, slog.LevelDebug, "run invoked", slogx.SensitiveData("messages", messages), slogx.SensitiveData("opts", opts))
		for update, err := range next(ctx, messages, opts...) {
			if err != nil {
				if errors.Is(err, context.Canceled) {
					l.log(ctx, slog.LevelDebug, "run canceled", "error", err)
				} else {
					l.log(ctx, slog.LevelError, "run failed", "error", err)
				}
			} else if l.l.SensitiveData {
				l.log(ctx, slog.LevelDebug, "run received update", slogx.SensitiveData("update", update))
			}
			if !yield(update, err) {
				return
			}
		}
		l.log(ctx, slog.LevelDebug, "run completed", "duration", time.Since(start).String())
	}
}

func (l *logger) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	agentCtx := ctx.Value(agentContextKey{})
	if agentCtx != nil {
		ac := agentCtx.(agentContext)
		args = append(args, "agentID", ac.ID)
		if ac.Name != "" {
			args = append(args, "agentName", ac.Name)
		}
	}
	l.l.Log(ctx, level, msg, args...)
}
