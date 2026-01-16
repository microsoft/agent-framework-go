// Copyright (c) Microsoft. All rights reserved.

package logger

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"time"

	"github.com/microsoft/agent-framework-go/agent"
	"github.com/microsoft/agent-framework-go/agent/agentopt"
	"github.com/microsoft/agent-framework-go/agent/middleware"
	"github.com/microsoft/agent-framework-go/internal/slogx"
	"github.com/microsoft/agent-framework-go/message"
)

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

func (l *logger) Run(next middleware.RunFunc, ctx context.Context, a agent.Agent, messages []*message.Message, opts ...agentopt.RunOption) iter.Seq2[*message.ResponseUpdate, error] {
	return func(yield func(*message.ResponseUpdate, error) bool) {
		start := time.Now()
		l.log(ctx, a, slog.LevelDebug, "run invoked", slogx.SensitiveData("messages", messages), slogx.SensitiveData("opts", opts))
		for update, err := range next(ctx, a, messages, opts...) {
			if err != nil {
				if errors.Is(err, context.Canceled) {
					l.log(ctx, a, slog.LevelDebug, "run canceled", "error", err)
				} else {
					l.log(ctx, a, slog.LevelError, "run failed", "error", err)
				}
			} else if l.l.SensitiveData {
				l.log(ctx, a, slog.LevelDebug, "run received update", slogx.SensitiveData("update", update))
			}
			if !yield(update, err) {
				return
			}
		}
		l.log(ctx, a, slog.LevelDebug, "run completed", "duration", time.Since(start).String())
	}
}

func (l *logger) log(ctx context.Context, a agent.Agent, level slog.Level, msg string, args ...any) {
	if a != nil {
		args = append(args, "agentID", a.Identity().ID())
		if a.Identity().Name() != "" {
			args = append(args, "agentName", a.Identity().Name())
		}
	}
	l.l.Log(ctx, level, msg, args...)
}
