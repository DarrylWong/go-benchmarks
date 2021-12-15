package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"runtime"
	"runtime/trace"
	"sort"
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
var doFluff bool

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

func main() {
	var traceFile string
	var howAllocated = "stack"
	var asBench bool

	flag.StringVar(&traceFile, "trace", traceFile, "name of trace file to create")
	flag.StringVar(&howAllocated, "how", howAllocated, "how the buffer is allocated = {stack,heap,global}")
	flag.BoolVar(&doFluff, "fluff", doFluff, "insert 'fluff' into allocation runs to break up sweeps")
	flag.BoolVar(&asBench, "bench", asBench, "output in Go benchmark format")
	flag.Parse()

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

	if asBench {
		fmt.Printf("goos: %s\n", runtime.GOOS)
		fmt.Printf("goarch: %s\n", runtime.GOARCH)

		width := int(math.Ceil(math.Log10(float64(1 + worst))))

		fmt.Printf("BenchmarkAverageLatency 1 %[1]*dns\n", width, time.Duration(total.Nanoseconds()/msgCount1))
		fmt.Printf("BenchmarkMedianLatency  1 %[1]*dns\n", width, delays[len(delays)/2])
		fmt.Printf("Benchmark99Latency      1 %[1]*dns\n", width, delays[int(0.99*float64(len(delays)))])
		fmt.Printf("Benchmark999Latency     1 %[1]*dns\n", width, delays[int(0.999*float64(len(delays)))])
		fmt.Printf("Benchmark9999Latency    1 %[1]*dns\n", width, delays[int(0.9999*float64(len(delays)))])
		fmt.Printf("Benchmark99999Latency   1 %[1]*dns\n", width, delays[int(0.99999*float64(len(delays)))])
		fmt.Printf("BenchmarkWorstLatency   1 %[1]*dns\n", width, worst)
	} else {

		fmt.Println("Worst allocation latency:", worst)
		fmt.Println("Worst allocation index:", worstIndex)
		fmt.Println("Worst allocation occurs at run elapsed time:", worstElapsed)
		fmt.Println("Average allocation latency:", time.Duration(total.Nanoseconds()/msgCount1))
		fmt.Println("Median allocation latency:", delays[len(delays)/2])
		fmt.Println("99% allocation latency:", delays[int(0.99*float64(len(delays)))])
		fmt.Println("99.9% allocation latency:", delays[int(0.999*float64(len(delays)))])
		fmt.Println("99.99% allocation latency:", delays[int(0.9999*float64(len(delays)))])
		fmt.Println("99.999% allocation latency:", delays[int(0.99999*float64(len(delays)))])
		fmt.Println("Sizeof(circularBuffer) =", unsafe.Sizeof(*c))
		fmt.Println("Approximate live memory =", unsafe.Sizeof(*c)+bufferLen*1024)
	}
}
