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

type LB struct {
	// Performance measurement stuff
	delays []time.Duration
	worst  time.Duration
	// For making sense of the bad outcome.
	total        time.Duration
	worstIndex   int
	allStart     time.Time
	worstElapsed time.Duration

	// How to allocate
	doFluff      bool
	howAllocated string
}

var fluff kbyte

// newSlice allocates a 1k slice of bytes and initializes them all to byte(n)
func (lb *LB) newSlice(n int) kbyte {
	m := make(kbyte, 1024)
	if lb.doFluff && n&63 == 0 {
		fluff = make(kbyte, 1024)
	}
	for i := range m {
		m[i] = byte(n)
	}
	return m
}

// storeSlice stores a newly allocated 1k slice of bytes at circularBuffer[count%bufferLen]
// (bufferLen is the length of the array circularBuffer)
// It also checks the time needed to do this and records the worst case.
func (lb *LB) storeSlice(c *circularBuffer, highID int) {
	start := time.Now()
	c[highID%bufferLen] = lb.newSlice(highID)
	elapsed := time.Since(start)

	candElapsed := time.Since(lb.allStart) // Record location of worst in trace
	if elapsed > lb.worst {
		lb.worst = elapsed
		lb.worstIndex = highID
		lb.worstElapsed = candElapsed
	}
	lb.total = time.Duration(lb.total.Nanoseconds() + elapsed.Nanoseconds())
	lb.delays = append(lb.delays, elapsed)
}

//go:noinline
func (lb *LB) work(c *circularBuffer, count int) {
	for i := 0; i < count; i++ {
		lb.storeSlice(c, i)
	}
}

func (lb *LB) doAllocations() {
	switch lb.howAllocated {
	case "stack":
		lb.stack()
	case "heap":
		lb.heap()
	case "global":
		lb.global()
	}
}

//go:noinline
func (lb *LB) stack() {
	var c circularBuffer
	lb.work(&c, msgCount1)
}

var global_c circularBuffer

//go:noinline
func (lb *LB) global() {
	lb.work(&global_c, msgCount1)
}

var sink *circularBuffer

//go:noinline
func (lb *LB) heap() {
	c := &circularBuffer{}
	sink = c // force to heap
	lb.work(c, msgCount1)
}

var traceFile string

func flags() (string, bool) {
	var howAllocated = "stack"
	var doFluff bool
	flag.StringVar(&traceFile, "trace", traceFile, "name of trace file to create")
	flag.StringVar(&howAllocated, "how", howAllocated, "how the buffer is allocated = {stack,heap,global}")
	flag.BoolVar(&doFluff, "fluff", doFluff, "insert 'fluff' into allocation runs to break up sweeps")

	flag.Parse()

	switch howAllocated {
	case "stack", "heap", "global":
		break
	default:
		fmt.Fprintf(os.Stderr, "-how needs to be one of 'heap', 'stack' or 'global, saw '%s' instead\n", howAllocated)
		os.Exit(1)
	}
	return howAllocated, doFluff
}

func (lb0 *LB) bench(b *testing.B) {
	var c *circularBuffer = &circularBuffer{}
	lb0.delays = make([]time.Duration, 0, msgCount1)
	// Warmup
	lb0.work(c, msgCount0)
	c = nil

	lb := &LB{doFluff: lb0.doFluff, howAllocated: lb0.howAllocated}

	if traceFile != "" {
		f, err := os.Create(traceFile)
		if err != nil {
			if b != nil {
				b.Fatalf("Could not create trace file '%s'\n", traceFile)
			} else {
				fmt.Fprintf(os.Stderr, "Could not create trace file '%s'\n", traceFile)
				os.Exit(1)
			}
		}
		defer f.Close()
		trace.Start(f)
		defer trace.Stop()
	}
	if b != nil {
		b.ResetTimer()
	}
	lb.allStart = time.Now()

	lb.doAllocations()

	sort.Slice(lb.delays, func(i, j int) bool { return lb.delays[i] < lb.delays[j] })

	delays := lb.delays
	delayLen := float64(len(delays))
	average, median := time.Duration(lb.total.Nanoseconds()/msgCount1), delays[len(delays)/2]
	p29, p39, p49, p59, p69 := lb.delays[int(0.99*delayLen)], delays[int(0.999*delayLen)], delays[int(0.9999*delayLen)], delays[int(0.99999*delayLen)], delays[int(0.999999*delayLen)]

	if b != nil {
		b.ReportMetric(float64(average.Nanoseconds()), "avg-ns")
		b.ReportMetric(float64(median), "median-ns")
		b.ReportMetric(float64(p29), "p29-ns")
		b.ReportMetric(float64(p39), "p39-ns")
		b.ReportMetric(float64(p49), "p49-ns")
		b.ReportMetric(float64(p59), "p59-ns")
		b.ReportMetric(float64(p69), "p69-ns")
		b.ReportMetric(float64(lb.worst), "worst-ns")
	} else {
		fmt.Printf("GC latency benchmark, how=%s, fluff=%v\n", lb.howAllocated, lb.doFluff)
		fmt.Println("Worst allocation latency:", lb.worst)
		fmt.Println("Worst allocation index:", lb.worstIndex)
		fmt.Println("Worst allocation occurs at run elapsed time:", lb.worstElapsed)
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
