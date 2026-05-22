// Copyright (c) Microsoft. All rights reserved.

//go:build !gc

package workflow

import "reflect"

func lookupRuntimeTypeByID(TypeID) (reflect.Type, bool) {
	return nil, false
}
