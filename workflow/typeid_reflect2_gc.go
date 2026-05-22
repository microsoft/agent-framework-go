// Copyright (c) Microsoft. All rights reserved.

//go:build gc

package workflow

import (
	"reflect"
	"sync"
	"unsafe"
)

//go:linkname reflectTypeLinks reflect.typelinks
func reflectTypeLinks() (sections []unsafe.Pointer, offsets [][]int32)

//go:linkname reflectResolveTypeOff reflect.resolveTypeOff
func reflectResolveTypeOff(rtype unsafe.Pointer, off int32) unsafe.Pointer

func lookupRuntimeTypeByID(id TypeID) (reflect.Type, bool) {
	typ, ok := runtimeTypes()[id]
	return typ, ok
}

var runtimeTypes = sync.OnceValue(func() map[TypeID]reflect.Type {
	types := make(map[TypeID]reflect.Type)
	var obj any = reflect.TypeFor[int]()
	sections, offsets := reflectTypeLinks()
	for sectionIndex, sectionOffsets := range offsets {
		section := sections[sectionIndex]
		for _, offset := range sectionOffsets {
			(*emptyInterface)(unsafe.Pointer(&obj)).word = reflectResolveTypeOff(section, offset)
			typ, ok := obj.(reflect.Type)
			if !ok {
				continue
			}
			rememberRuntimeType(types, typ)
		}
	}
	return types
})

func rememberRuntimeType(types map[TypeID]reflect.Type, typ reflect.Type) {
	base, ok := dereferencePointerType(typ)
	if !ok || base == nil {
		return
	}
	if base.Kind() == reflect.Interface {
		storeRuntimeType(types, base)
		return
	}
	storeRuntimeType(types, base)
	storeRuntimeType(types, reflect.PointerTo(base))
}

func storeRuntimeType(types map[TypeID]reflect.Type, typ reflect.Type) {
	if typ == nil {
		return
	}
	id := typeIDOf(typ)
	if id == (TypeID{}) {
		return
	}
	if current, ok := types[id]; !ok || preferRuntimeType(typ, current) {
		types[id] = typ
	}
}

type emptyInterface struct {
	typ  unsafe.Pointer
	word unsafe.Pointer
}
