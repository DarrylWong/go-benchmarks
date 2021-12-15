// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"testing"
)

type testCase struct {
	howAlloc  string
	withFluff bool
}

func BenchmarkGCLatency(b *testing.B) {
	tcs := []testCase{
		{"stack", false},
		{"heap", false},
		{"global", false},
		{"stack", true},
		{"heap", true},
		{"global", true},
	}

	for _, tc := range tcs {
		lb0 := &LB{doFluff: tc.withFluff, howAllocated: tc.howAlloc}
		b.Run(fmt.Sprintf("how=%s-fluff=%v", tc.howAlloc, tc.withFluff),
			func(b *testing.B) { lb0.bench(b) })
	}
}
