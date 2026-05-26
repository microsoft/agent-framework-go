// Copyright (c) Microsoft. All rights reserved.

package workflow

import "testing"

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
