// Copyright (c) Microsoft. All rights reserved.

package foundryprovider

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIsCallerCancellation(t *testing.T) {
	tests := []struct {
		name   string
		ctxErr error
		err    error
		want   bool
	}{
		{name: "unrelated provider failure after cancellation", ctxErr: context.Canceled, err: errors.New("provider failed")},
		{name: "cancellation error without caller cancellation", err: context.Canceled},
		{name: "caller cancellation", ctxErr: context.Canceled, err: context.Canceled, want: true},
		{name: "caller deadline", ctxErr: context.DeadlineExceeded, err: context.DeadlineExceeded, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var ctx context.Context
			var cancel context.CancelFunc
			switch test.ctxErr {
			case context.Canceled:
				ctx, cancel = context.WithCancel(t.Context())
				cancel()
			case context.DeadlineExceeded:
				ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
			default:
				ctx, cancel = context.WithCancel(t.Context())
			}
			defer cancel()
			if got := isCallerCancellation(ctx, test.err); got != test.want {
				t.Fatalf("isCallerCancellation() = %t, want %t", got, test.want)
			}
		})
	}
}
