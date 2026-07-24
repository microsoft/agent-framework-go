// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"errors"
)

// ErrInvalidInputType is returned (wrapped) when a message enqueued as
// workflow input has a type that the workflow's start executor does not
// accept. Callers can test for it with errors.Is.
var ErrInvalidInputType = errors.New("invalid workflow input type")
