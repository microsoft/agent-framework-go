// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"fmt"
	"reflect"
	"sync"
)

var typeIDRegistry sync.Map

// TypeID is a representation of a type's identity, including its package and
// type names. Pointer types are identified by their element type.
type TypeID struct {
	// PackageName is the package path for named Go types. It is empty for
	// built-in and unnamed types.
	PackageName string

	// TypeName is the type name for named Go types, or the string form for
	// unnamed types such as pointers, slices, and maps.
	TypeName string
}

// NewTypeID creates a TypeID for typ and caches typ as a runtime type that can
// be resolved from that identity. A nil type returns the zero TypeID. Pointer
// types are identified by their element type.
func NewTypeID(typ reflect.Type) TypeID {
	id := typeIDOf(typ)
	cacheRuntimeType(id, typ)
	return id
}

func typeIDOf(typ reflect.Type) TypeID {
	var ok bool
	typ, ok = dereferencePointerType(typ)
	if !ok || typ == nil {
		return TypeID{}
	}
	name := typ.Name()
	if name == "" {
		name = typ.String()
	}
	id := TypeID{
		PackageName: typ.PkgPath(),
		TypeName:    name,
	}
	return id
}

func cacheRuntimeType(id TypeID, typ reflect.Type) {
	if id == (TypeID{}) || typ == nil {
		return
	}
	candidate := runtimeTypeForCache(typ)
	if candidate == nil {
		return
	}
	current, loaded := typeIDRegistry.LoadOrStore(id, candidate)
	if !loaded || preferRuntimeType(candidate, current.(reflect.Type)) {
		typeIDRegistry.Store(id, candidate)
	}
}

func runtimeTypeForCache(typ reflect.Type) reflect.Type {
	if typ == nil {
		return nil
	}
	wasPointer := typ.Kind() == reflect.Pointer
	base, ok := dereferencePointerType(typ)
	if !ok || base == nil {
		return nil
	}
	if !wasPointer || base.Kind() == reflect.Interface {
		return base
	}
	return reflect.PointerTo(base)
}

func dereferencePointerType(typ reflect.Type) (reflect.Type, bool) {
	if typ == nil {
		return nil, true
	}
	slowpoke := typ
	indir := 0
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
		if typ == slowpoke {
			return nil, false
		}
		if indir%2 == 0 {
			slowpoke = slowpoke.Elem()
		}
		indir++
	}
	return typ, true
}

// preferRuntimeType reports whether candidate should replace current in the
// TypeID runtime cache. Interface types are preferred because they enable
// polymorphic matching, and pointer concrete types are preferred over value
// concrete types because TypeID canonicalizes both to the element identity.
func preferRuntimeType(candidate, current reflect.Type) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}
	if candidate.Kind() == reflect.Interface {
		return current.Kind() != reflect.Interface
	}
	if current.Kind() == reflect.Interface {
		return false
	}
	return candidate.Kind() == reflect.Pointer && current.Kind() != reflect.Pointer
}

// Match reports whether typ has the same package and type names as t.
// The zero TypeID represents an unknown type and does not match any type.
func (t TypeID) Match(typ reflect.Type) bool {
	if t == (TypeID{}) {
		return false
	}
	return NewTypeID(typ) == t
}

// MatchPolymorphic reports whether typ either matches this type identity
// exactly or can be assigned to the runtime type cached or discovered for this
// identity. Concrete types can match interface response ports when the interface
// type has been cached through [NewTypeID] or discovered from runtime type
// metadata.
func (t TypeID) MatchPolymorphic(typ reflect.Type) bool {
	if t.Match(typ) {
		return true
	}
	if typ == nil || t == (TypeID{}) {
		return false
	}
	target, ok := runtimeTypeForTypeID(t)
	return ok && typ.AssignableTo(target)
}

func runtimeTypeForTypeID(id TypeID) (reflect.Type, bool) {
	if id == (TypeID{}) {
		return nil, false
	}
	typ, ok := typeIDRegistry.Load(id)
	if ok {
		return typ.(reflect.Type), true
	}
	fallback, ok := lookupRuntimeTypeByID(id)
	if !ok {
		return nil, false
	}
	cacheRuntimeType(id, fallback)
	typ, ok = typeIDRegistry.Load(id)
	if ok {
		return typ.(reflect.Type), true
	}
	return fallback, true
}

// String returns the type identity in a compact diagnostic form.
func (t TypeID) String() string {
	if t.PackageName == "" {
		return t.TypeName
	}
	return fmt.Sprintf("%s, %s", t.TypeName, t.PackageName)
}
