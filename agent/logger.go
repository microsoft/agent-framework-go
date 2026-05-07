// Copyright (c) Microsoft. All rights reserved.

package agent

import (
	"context"
	"errors"
	"iter"
	"log/slog"
	"time"

	"github.com/microsoft/agent-framework-go/internal/slogx"
	"github.com/microsoft/agent-framework-go/message"
)

func newRunLoggerMiddleware(logger *slog.Logger, sensitiveData bool) Middleware {
	return &runLoggerMiddleware{l: slogx.Logger{
		Logger:        logger,
		SensitiveData: sensitiveData,
		Type:          slogx.TypeMiddleware,
		Name:          "logger",
	}}
}

type runLoggerMiddleware struct {
	l slogx.Logger
}

func (l *runLoggerMiddleware) Run(next RunFunc, ctx context.Context, messages []*message.Message, opts ...Option) iter.Seq2[*ResponseUpdate, error] {
	return func(yield func(*ResponseUpdate, error) bool) {
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

func (l *runLoggerMiddleware) log(ctx context.Context, level slog.Level, msg string, args ...any) {
	a, ok := AgentFromContext(ctx)
	if ok {
		args = append(args, "agentID", a.ID())
		if a.Name() != "" {
			args = append(args, "agentName", a.Name())
		}
	}
	l.l.Log(ctx, level, msg, args...)
}
