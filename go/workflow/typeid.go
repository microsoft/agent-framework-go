// Copyright (c) Microsoft. All rights reserved.

package workflow

import "reflect"

type TypeID struct {
	PackageName string
	TypeName    string
}

func NewTypeID(typ reflect.Type) TypeID {
	name := typ.Name()
	if name == "" {
		name = typ.String()
	}
	return TypeID{
		PackageName: typ.PkgPath(),
		TypeName:    name,
	}
}

func (t TypeID) Match(typ reflect.Type) bool {
	return NewTypeID(typ) == t
}

func (t TypeID) IsZero() bool {
	return t.PackageName == "" && t.TypeName == ""
}
