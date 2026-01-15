// Copyright (c) Microsoft. All rights reserved.

package slogx

import (
	"context"
	"log/slog"
	"slices"
)

type Type int

const (
	TypeNone Type = iota
	TypeMiddleware
)

type sensitiveData struct {
	any
}

func SensitiveData(key string, value any) slog.Attr {
	return slog.Any(key, sensitiveData{value})
}

type Logger struct {
	Logger        *slog.Logger
	Type          Type
	Name          string
	SensitiveData bool
}

func (l *Logger) Debug(ctx context.Context, msg string, args ...any) {
	l.Log(ctx, slog.LevelDebug, msg, args...)
}

func (l *Logger) Info(ctx context.Context, msg string, args ...any) {
	l.Log(ctx, slog.LevelInfo, msg, args...)
}

func (l *Logger) Warn(ctx context.Context, msg string, args ...any) {
	l.Log(ctx, slog.LevelWarn, msg, args...)
}

func (l *Logger) Error(ctx context.Context, msg string, args ...any) {
	l.Log(ctx, slog.LevelError, msg, args...)
}

func (l *Logger) Log(ctx context.Context, level slog.Level, msg string, args ...any) {
	if l.Logger == nil {
		return
	}
	if !l.Logger.Enabled(ctx, level) {
		return
	}
	switch l.Type {
	case TypeMiddleware:
		args = append(args, "middleware", l.Name)
	}
	if !l.SensitiveData {
		args = slices.DeleteFunc(args, func(v any) bool {
			if v, ok := v.(slog.Attr); ok {
				_, isSensitive := v.Value.Any().(sensitiveData)
				return isSensitive
			}
			return false
		})
	}
	l.Logger.Log(ctx, level, msg, args...)
}
