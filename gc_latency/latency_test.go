package main

import (
	"testing"
)

func BenchmarkGCLatency(b *testing.B) {
	asBench = true
	bench(b)
}
