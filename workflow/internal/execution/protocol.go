// Copyright (c) Microsoft. All rights reserved.

package execution

import (
	"reflect"
	"slices"

	"github.com/microsoft/agent-framework-go/workflow"
)

// DeclaredSendType returns the protocol type that should be used when sending
// a message of typ from source. Exact declarations win over broad any
// declarations. Other interface send declarations match only that exact
// interface type.
func DeclaredSendType(source *workflow.Executor, typ reflect.Type) (reflect.Type, bool) {
	if source == nil || typ == nil {
		return nil, false
	}
	return declaredSendType(sentTypes(source), typ)
}

// SentRuntimeType returns the runtime type declared by source's send protocol
// for typeID. It includes workflow system messages that are always sendable but
// omitted from public protocol descriptors.
func SentRuntimeType(source *workflow.Executor, typeID workflow.TypeID) (reflect.Type, bool) {
	if source == nil || typeID == (workflow.TypeID{}) {
		return nil, false
	}
	return sentRuntimeType(sentTypes(source), typeID)
}

// CanHandleType reports whether target's protocol accepts typ.
func CanHandleType(target *workflow.Executor, typ reflect.Type) bool {
	if target == nil || typ == nil {
		return false
	}
	return canHandleType(target.DescribeProtocol(), typ)
}

// CanHandleTypeID reports whether target's protocol accepts typeID.
func CanHandleTypeID(target *workflow.Executor, typeID workflow.TypeID) bool {
	if target == nil || typeID == (workflow.TypeID{}) {
		return false
	}
	return canHandleTypeID(target.DescribeProtocol(), typeID)
}

// CanOutputType reports whether source's protocol can yield typ as workflow output.
func CanOutputType(source *workflow.Executor, typ reflect.Type) bool {
	if source == nil || typ == nil {
		return false
	}
	return canOutputType(source.DescribeProtocol(), typ)
}

func sentTypes(source *workflow.Executor) []reflect.Type {
	descriptor := source.DescribeProtocol()
	types := slices.Clone(descriptor.Sends)
	return appendUniqueTypes(types, knownSentTypes()...)
}

func canHandleType(descriptor workflow.ProtocolDescriptor, typ reflect.Type) bool {
	if typ == nil {
		return false
	}
	if descriptor.AcceptsAll {
		return true
	}
	return slices.ContainsFunc(descriptor.Accepts, func(candidate reflect.Type) bool {
		return workflow.NewTypeID(candidate).MatchPolymorphic(typ)
	})
}

func canHandleTypeID(descriptor workflow.ProtocolDescriptor, typeID workflow.TypeID) bool {
	if typeID == (workflow.TypeID{}) {
		return false
	}
	if descriptor.AcceptsAll {
		return true
	}
	return slices.ContainsFunc(descriptor.Accepts, func(candidate reflect.Type) bool {
		return workflow.NewTypeID(candidate) == typeID
	})
}

func canOutputType(descriptor workflow.ProtocolDescriptor, typ reflect.Type) bool {
	if typ == nil {
		return false
	}
	return slices.ContainsFunc(descriptor.Yields, func(candidate reflect.Type) bool {
		return workflow.NewTypeID(candidate).MatchPolymorphic(typ)
	})
}

func knownSentTypes() []reflect.Type {
	return []reflect.Type{
		reflect.TypeFor[*workflow.ExternalRequest](),
		reflect.TypeFor[*workflow.ExternalResponse](),
	}
}

func declaredSendType(sentTypes []reflect.Type, typ reflect.Type) (reflect.Type, bool) {
	for _, candidate := range sentTypes {
		if typ == candidate {
			return candidate, true
		}
	}
	for _, candidate := range sentTypes {
		if declaredSendTypeAssignableTo(typ, candidate) {
			return candidate, true
		}
	}
	for _, candidate := range sentTypes {
		if candidate == reflect.TypeFor[any]() {
			return candidate, true
		}
	}
	return nil, false
}

func declaredSendTypeAssignableTo(typ reflect.Type, candidate reflect.Type) bool {
	if typ == nil || candidate == nil || candidate.Kind() == reflect.Interface {
		return false
	}
	return typ.AssignableTo(candidate)
}

func sentRuntimeType(sentTypes []reflect.Type, typeID workflow.TypeID) (reflect.Type, bool) {
	var result reflect.Type
	found := false
	for _, typ := range sentTypes {
		if workflow.NewTypeID(typ) != typeID {
			continue
		}
		if !found || preferRuntimeType(typ, result) {
			result = typ
			found = true
		}
	}
	return result, found
}

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

func appendUniqueTypes(dst []reflect.Type, src ...reflect.Type) []reflect.Type {
	for _, typ := range src {
		if typ == nil || slices.Contains(dst, typ) {
			continue
		}
		dst = append(dst, typ)
	}
	return dst
}
