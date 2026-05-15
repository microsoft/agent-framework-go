// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"fmt"
	"reflect"
)

// TypeID is a representation of a type's identity, including its package and
// type names.
type TypeID struct {
	// PackageName is the package path for named Go types. It is empty for
	// built-in and unnamed types.
	PackageName string

	// TypeName is the type name for named Go types, or the string form for
	// unnamed types such as pointers, slices, and maps.
	TypeName string
}

// NewTypeID creates a TypeID for typ. A nil type returns the zero TypeID.
func NewTypeID(typ reflect.Type) TypeID {
	if typ == nil {
		return TypeID{}
	}
	name := typ.Name()
	if name == "" {
		name = typ.String()
	}
	return TypeID{
		PackageName: typ.PkgPath(),
		TypeName:    name,
	}
}

// Match reports whether typ has the same package and type names as t.
// The zero TypeID represents an unknown type and does not match any type.
func (t TypeID) Match(typ reflect.Type) bool {
	if t == (TypeID{}) {
		return false
	}
	return NewTypeID(typ) == t
}

// String returns the type identity in a compact diagnostic form.
func (t TypeID) String() string {
	if t.PackageName == "" {
		return t.TypeName
	}
	return fmt.Sprintf("%s, %s", t.TypeName, t.PackageName)
}
