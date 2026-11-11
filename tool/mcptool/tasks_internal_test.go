// Copyright (c) Microsoft. All rights reserved.

package mcptool

import (
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestTaskOptions_DefaultsAreSane(t *testing.T) {
	var options TaskOptions
	if err := options.validate(); err != nil {
		t.Fatal(err)
	}
	if options.DisableCancelRemoteTaskOnLocalCancellation {
		t.Fatal("DisableCancelRemoteTaskOnLocalCancellation = true, want false")
	}
	if options.MaxConsecutiveStuckPolls != 60 {
		t.Fatalf("MaxConsecutiveStuckPolls = %d, want 60", options.MaxConsecutiveStuckPolls)
	}
	if options.MaxTotalInputRequests != 100 {
		t.Fatalf("MaxTotalInputRequests = %d, want 100", options.MaxTotalInputRequests)
	}
	if options.RemoteCancellationTimeout != 5*time.Second {
		t.Fatalf("RemoteCancellationTimeout = %s, want 5s", options.RemoteCancellationTimeout)
	}
	if options.MinimumPollingInterval != 10*time.Millisecond {
		t.Fatalf("MinimumPollingInterval = %s, want 10ms", options.MinimumPollingInterval)
	}
	maximum := time.Duration((1<<63 - 1) / int64(time.Millisecond) * int64(time.Millisecond))
	if options.MaximumPollingInterval != maximum {
		t.Fatalf("MaximumPollingInterval = %s, want %s", options.MaximumPollingInterval, maximum)
	}
}

func TestTaskOptions_ExplicitValuesArePreserved(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(disabled), func(t *testing.T) {
			options := TaskOptions{
				DisableCancelRemoteTaskOnLocalCancellation: disabled,
				MaxConsecutiveStuckPolls:                   2,
				MaxTotalInputRequests:                      3,
				RemoteCancellationTimeout:                  time.Second,
				MinimumPollingInterval:                     time.Millisecond,
				MaximumPollingInterval:                     100 * time.Millisecond,
			}
			want := options
			if err := options.validate(); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(options, want) {
				t.Fatalf("options = %+v, want %+v", options, want)
			}
		})
	}
}
