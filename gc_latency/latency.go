// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/trace"
	"sort"
	"testing"
	"time"
	"unsafe"
)

const (
	bufferLen = 200000
	msgCount0 = 1000000 // warmup
	msgCount1 = 5000000 // run (5 million)
)

type kbyte []byte
type circularBuffer [bufferLen]kbyte

var worst time.Duration

// For making sense of the bad outcome.
var total time.Duration
var worstIndex int
var allStart time.Time
var worstElapsed time.Duration

var fluff kbyte
var doFluff, doFluffFlag bool

// newSlice allocates a 1k slice of bytes and initializes them all to byte(n)
func newSlice(n int) kbyte {
	m := make(kbyte, 1024)
	if doFluff && n&63 == 0 {
		fluff = make(kbyte, 1024)
	}
	for i := range m {
		m[i] = byte(n)
	}
	return m
}

var delays []time.Duration

// storeSlice stores a newly allocated 1k slice of bytes at circularBuffer[count%bufferLen]
// (bufferLen is the length of the array circularBuffer)
// It also checks the time needed to do this and records the worst case.
func storeSlice(c *circularBuffer, highID int) {
	start := time.Now()
	c[highID%bufferLen] = newSlice(highID)
	elapsed := time.Since(start)

	candElapsed := time.Since(allStart) // Record location of worst in trace
	if elapsed > worst {
		worst = elapsed
		worstIndex = highID
		worstElapsed = candElapsed
	}
	total = time.Duration(total.Nanoseconds() + elapsed.Nanoseconds())
	delays = append(delays, elapsed)
}

//go:noinline
func work(c *circularBuffer, count int) {
	for i := 0; i < count; i++ {
		storeSlice(c, i)
	}
}

//go:noinline
func stack() {
	var c circularBuffer
	work(&c, msgCount1)
}

var global_c circularBuffer

//go:noinline
func global() {
	work(&global_c, msgCount1)
}

var sink *circularBuffer

//go:noinline
func heap() {
	c := &circularBuffer{}
	sink = c // force to heap
	work(c, msgCount1)
}

var traceFile string
var howAllocated = "stack"

func flags() {
	flag.StringVar(&traceFile, "trace", traceFile, "name of trace file to create")
	flag.StringVar(&howAllocated, "how", howAllocated, "how the buffer is allocated = {stack,heap,global}")
	flag.BoolVar(&doFluffFlag, "fluff", doFluffFlag, "insert 'fluff' into allocation runs to break up sweeps")
	flag.Parse()
}

func bench(b *testing.B, howAllocated string, doFluffArg bool) {
	doFluff = doFluffArg
	var c *circularBuffer = &circularBuffer{}
	delays = make([]time.Duration, 0, msgCount1)
	// Warmup
	work(c, msgCount0)
	c = nil
	delays = delays[:0]

	total = time.Duration(0)
	worstElapsed = time.Duration(0)
	worst = time.Duration(0)
	worstIndex = 0

	if traceFile != "" {
		f, err := os.Create(traceFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not create trace file '%s'\n", traceFile)
			os.Exit(1)
		}
		defer f.Close()
		trace.Start(f)
		defer trace.Stop()
	}
	if b != nil {
		b.ResetTimer()
	}
	allStart = time.Now()
	c = nil

	switch howAllocated {
	case "stack":
		stack()
	case "heap":
		heap()
	case "global":
		global()
	default:
		fmt.Fprintf(os.Stderr, "-how needs to be one of 'heap', 'stack' or 'global, saw '%s' instead\n", howAllocated)
		os.Exit(1)
	}

	sort.Slice(delays, func(i, j int) bool { return delays[i] < delays[j] })

	average, median := time.Duration(total.Nanoseconds()/msgCount1), delays[len(delays)/2]
	p29, p39, p49, p59, p69 := delays[int(0.99*float64(len(delays)))], delays[int(0.999*float64(len(delays)))], delays[int(0.9999*float64(len(delays)))], delays[int(0.99999*float64(len(delays)))], delays[int(0.999999*float64(len(delays)))]

	if b != nil {
		b.ReportMetric(float64(average.Nanoseconds()), "avg-ns")
		b.ReportMetric(float64(median), "median-ns")
		b.ReportMetric(float64(p29), "p29-ns")
		b.ReportMetric(float64(p39), "p39-ns")
		b.ReportMetric(float64(p49), "p49-ns")
		b.ReportMetric(float64(p59), "p59-ns")
		b.ReportMetric(float64(p69), "p69-ns")
		b.ReportMetric(float64(worst), "worst-ns")
	} else {
		fmt.Printf("GC latency benchmark, how=%s, fluff=%v\n", howAllocated, doFluffArg)
		fmt.Println("Worst allocation latency:", worst)
		fmt.Println("Worst allocation index:", worstIndex)
		fmt.Println("Worst allocation occurs at run elapsed time:", worstElapsed)
		fmt.Println("Average allocation latency:", average)
		fmt.Println("Median allocation latency:", median)
		fmt.Println("99% allocation latency:", p29)
		fmt.Println("99.9% allocation latency:", p39)
		fmt.Println("99.99% allocation latency:", p49)
		fmt.Println("99.999% allocation latency:", p59)
		fmt.Println("Sizeof(circularBuffer) =", unsafe.Sizeof(*c))
		fmt.Println("Approximate live memory =", unsafe.Sizeof(*c)+bufferLen*1024)
	}
}
