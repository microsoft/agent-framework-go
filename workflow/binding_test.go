// Copyright (c) Microsoft. All rights reserved.

package workflow

import (
	"reflect"
	"testing"
)

func TestRequestPortBind_PanicsForInvalidPort(t *testing.T) {
	stringType := reflect.TypeFor[string]()
	tests := []struct {
		name string
		port RequestPort
		want string
	}{
		{
			name: "empty ID",
			port: RequestPort{Request: stringType, Response: stringType},
			want: "workflow: request port ID is required",
		},
		{
			name: "nil request type",
			port: RequestPort{ID: "port", Response: stringType},
			want: `workflow: request port "port" request type is required`,
		},
		{
			name: "nil response type",
			port: RequestPort{ID: "port", Request: stringType},
			want: `workflow: request port "port" response type is required`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != test.want {
					t.Fatalf("panic = %v, want %q", got, test.want)
				}
			}()
			test.port.Bind()
		})
	}
}

func TestIsAnonymousFuncName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "github.com/example/project/pkg.Named", want: false},
		{name: "github.com/example/project/pkg.funcNamed", want: false},
		{name: "github.com/example/project/pkg.func1", want: true},
		{name: "github.com/example/project/pkg.Named.func1", want: true},
		{name: "github.com/example/project/pkg.Named.func12.1", want: true},
		{name: "github.com/example/project/pkg.(*Type).Method-fm", want: false},
		{name: "github.com/example/project/pkg.(*Type).Method.func2", want: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isAnonymousFuncName(test.name); got != test.want {
				t.Fatalf("isAnonymousFuncName(%q) = %v, want %v", test.name, got, test.want)
			}
		})
	}
}
