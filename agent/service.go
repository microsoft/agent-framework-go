// Copyright (c) Microsoft. All rights reserved.

package agent

import "reflect"

// GetService asks provider for a service of type T identified by an optional key.
// The boolean reports whether a service of the requested type was found.
//
// A nil key requests the provider's default service. For an [Agent], this
// returns the agent itself when T is assignable from *Agent.
func GetService[T any](provider interface {
	GetService(reflect.Type, any) any
}, serviceKey any) (T, bool) {
	service := provider.GetService(reflect.TypeFor[T](), serviceKey)
	if service == nil {
		var zero T
		return zero, false
	}

	value, ok := service.(T)
	return value, ok
}

// GetService returns the service of the requested type, or nil when no
// matching service is available. A nil serviceType is invalid.
func (a *Agent) GetService(serviceType reflect.Type, serviceKey any) any {
	if serviceType == nil {
		panic("agent: nil service type")
	}
	if serviceKey == nil && reflect.TypeOf(a).AssignableTo(serviceType) {
		return a
	}
	return nil
}
