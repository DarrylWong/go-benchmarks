// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !wasm

package main

import (
	"bytes"
	"flag"
	"fmt"
	"github.com/pkg/errors"
	"golang.org/x/benchmarks/sweet/benchmarks/internal/driver"
	"golang.org/x/benchmarks/sweet/benchmarks/internal/server"
	"golang.org/x/benchmarks/sweet/common/diagnostics"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// Arbitrarily chosen as a time that seemed "right".
	waitTimeAfterStart = time.Second * 5
	// Arbitrarily chosen to match the cockroachdb default.
	basePort = 26257
	// The percentage of memory to allocate to the cache.
	cacheSize = "0.25"
)

type config struct {
	host           string
	cockroachdbBin string
	tmpDir         string
	benchName      string
	isProfiling    bool
	short          bool
	procsPerInst   int
	bench          *benchmark
}

var cliCfg config

func init() {
	driver.SetFlags(flag.CommandLine)
	flag.StringVar(&cliCfg.host, "host", "127.0.0.1", "hostname of cockroachdb server")
	flag.StringVar(&cliCfg.cockroachdbBin, "cockroachdb-bin", "", "path to cockroachdb binary")
	flag.StringVar(&cliCfg.tmpDir, "tmp", "", "path to temporary directory")
	flag.StringVar(&cliCfg.benchName, "bench", "", "name of the benchmark to run")
	flag.BoolVar(&cliCfg.short, "short", false, "whether to run a short version of this benchmark")
}

type cockroachdbInstance struct {
	name     string
	sqlPort  int
	httpPort int
	cmd      *exec.Cmd
	output   bytes.Buffer
}

func clusterAddresses(instances []*cockroachdbInstance) string {
	var s []string
	for _, inst := range instances {
		s = append(s, inst.sqlAddr())
	}
	return strings.Join(s, ",")
}

func launchCockroachCluster(cfg *config) ([]*cockroachdbInstance, error) {
	var instances []*cockroachdbInstance
	for i := 0; i < cfg.bench.nodeCount; i++ {
		instances = append(instances, &cockroachdbInstance{
			name:     fmt.Sprintf("roach-node-%d", i+1),
			sqlPort:  basePort + 2*i,
			httpPort: basePort + 2*i + 1,
		})
	}

	for n, inst := range instances {
		allOtherInstances := append([]*cockroachdbInstance{}, instances[:n]...)
		allOtherInstances = append(allOtherInstances, instances[n+1:]...)
		join := fmt.Sprintf("--join=%s", clusterAddresses(allOtherInstances))

		inst.cmd = exec.Command(cfg.cockroachdbBin,
			"start",
			"--insecure",
			"--listen-addr", inst.sqlAddr(),
			"--http-addr", inst.httpAddr(),
			"--cache", cacheSize,
			//"--temp-dir", cfg.tmpDir,
			"--logtostderr",
			join,
		)
		inst.cmd.Env = append(os.Environ(),
			fmt.Sprintf("GOMAXPROCS=%d", cfg.procsPerInst),
		)
		inst.cmd.Stdout = &inst.output
		inst.cmd.Stderr = &inst.output
		fmt.Printf("starting instance %q with cmd: %s\n", inst.name, inst.cmd.String())
		if err := inst.cmd.Start(); err != nil {
			return nil, fmt.Errorf("failed to start instance %q: %v", inst.name, err)
		}
	}
	time.Sleep(20 * time.Second)
	inst1 := instances[0]
	initCmd := exec.Command(cfg.cockroachdbBin,
		"init",
		"--insecure",
		fmt.Sprintf("--host=%s", cfg.host),
		fmt.Sprintf("--port=%d", inst1.sqlPort),
	)
	initCmd.Env = append(os.Environ(),
		fmt.Sprintf("GOMAXPROCS=%d", cfg.procsPerInst),
	)
	initCmd.Stdout = &inst1.output
	initCmd.Stderr = &inst1.output
	if err := initCmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to init instance %q: %v", inst1.name, err)
	}
	return instances, nil
}

func launchSingleNodeCluster(cfg *config) ([]*cockroachdbInstance, error) {
	var instances []*cockroachdbInstance
	instances = append(instances, &cockroachdbInstance{
		name:     "roach-node",
		sqlPort:  basePort,
		httpPort: basePort + 1,
	})
	inst := instances[0]

	inst.cmd = exec.Command(cfg.cockroachdbBin,
		"start-single-node",
		"--insecure",
		"--listen-addr", inst.sqlAddr(),
		"--http-addr", inst.httpAddr(),
		"--cache", cacheSize,
		"--temp-dir", cfg.tmpDir,
		"--logtostderr",
	)
	inst.cmd.Env = append(os.Environ(),
		fmt.Sprintf("GOMAXPROCS=%d", cfg.procsPerInst),
	)
	inst.cmd.Stdout = &inst.output
	inst.cmd.Stderr = &inst.output
	fmt.Printf("starting instance %q with cmd: %s\n", inst.name, inst.cmd.String())
	if err := inst.cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start instance %q: %v", inst.name, err)
	}
	return instances, nil
}

func (i *cockroachdbInstance) ping(cfg *config) error {
	// Wait until all cockroach instances have spun up.
	i.cmd = exec.Command(cfg.cockroachdbBin,
		"sql",
		"-e",
		"'SHOW TABLES'",
		"--host", i.sqlAddr(),
		"--insecure",
	)
	i.cmd.Stdout = &i.output
	i.cmd.Stderr = &i.output
	if err := i.cmd.Run(); err != nil {
		fmt.Printf("error pinging cockroach instance %s\n", i.output.String())
		return err
	}
	return nil
}

func (i *cockroachdbInstance) sqlAddr() string {
	return fmt.Sprintf("%s:%d", cliCfg.host, i.sqlPort)
}

func (i *cockroachdbInstance) httpAddr() string {
	return fmt.Sprintf("%s:%d", cliCfg.host, i.httpPort)
}

func (i *cockroachdbInstance) shutdown() error {
	// Only attempt to shut down the process if it's still running.
	if i.cmd != nil {
		if err := i.cmd.Process.Signal(os.Interrupt); err != nil {
			return err
		}
		if _, err := i.cmd.Process.Wait(); err != nil {
			return err
		}
	}
	return nil
}

type benchmark struct {
	name       string
	reportName string
	workload   string
	nodeCount  int
	args       []string
	longArgs   []string // if !config.short
	shortArgs  []string // if config.short
}

var benchmarks = []benchmark{
	{
		name:       "kv95/nodes=1",
		reportName: "CockroachDBkv95/nodes=1",
		workload:   "kv",
		nodeCount:  1,
		args: []string{
			"workload", "run", "kv",
			"--init",
			"--read-percent=95",
			"--min-block-bytes=1024",
			"--max-block-bytes=1024",
			"--concurrency=10000",
			"--max-rate=30000",
		},
		longArgs: []string{
			"--ramp=1m",
			"--duration=5m",
		},
		shortArgs: []string{
			"--ramp=15s",
			"--duration=1m",
		},
	},
	{
		name:       "kv95/nodes=3",
		reportName: "CockroachDBkv95/nodes=3",
		workload:   "kv",
		nodeCount:  3,
		args: []string{
			"workload", "run", "kv",
			"--init",
			"--read-percent=95",
			"--min-block-bytes=1024",
			"--max-block-bytes=1024",
			"--concurrency=10000",
			"--max-rate=30000",
		},
		longArgs: []string{
			"--ramp=1m",
			"--duration=5m",
		},
		shortArgs: []string{
			"--ramp=15s",
			"--duration=1m",
		},
	},
}

func runBenchmark(b *driver.B, cfg *config, instances []*cockroachdbInstance) (err error) {
	var pgurls []string
	for _, inst := range instances[:cfg.bench.nodeCount] {
		host := inst.sqlAddr()
		pgurls = append(pgurls, fmt.Sprintf("'postgres://root@%s?sslmode=disable'", host))
	}
	args := cfg.bench.args
	if cfg.short {
		args = append(args, cfg.bench.shortArgs...)
	} else {
		args = append(args, cfg.bench.longArgs...)
	}
	args = append(args, fmt.Sprintf("%s", strings.Join(pgurls, " ")))
	cmd := exec.Command(cfg.cockroachdbBin, args...)
	fmt.Fprintln(os.Stderr, cmd.String())

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), fmt.Sprintf("GOMAXPROCS=%d", cfg.procsPerInst))

	defer func() {
		if err != nil && stderr.Len() != 0 {
			fmt.Fprintln(os.Stderr, "=== Benchmarking tool stderr ===")
			fmt.Fprintln(os.Stderr, stderr.String())
		}
	}()

	b.ResetTimer()
	fmt.Println("darryl: about to run benchmark")
	if err := cmd.Run(); err != nil {
		fmt.Printf("darryl: benchmark failed with err:%s\n", err.Error())
		return err
	}
	b.StopTimer()

	for _, inst := range instances[:cfg.bench.nodeCount] {
		_ = inst.cmd.Process.Kill()
	}

	fmt.Println("darryl: benchmark done, collecting results")
	return reportFromBenchmarkOutput(b, stdout.String())
}

func reportFromBenchmarkOutput(b *driver.B, output string) (err error) {
	defer func() {
		if err != nil {
			fmt.Fprintln(os.Stderr, "=== Benchmarking tool output ===")
			fmt.Fprintln(os.Stderr, output)
		}
	}()

	err = getAndReportReadMetrics(b, output)
	if err != nil {
		return err
	}
	err = getAndReportWriteMetrics(b, output)
	if err != nil {
		return err
	}
	return nil
}

type benchmarkMetrics struct {
	totalOps       uint64
	opsPerSecond   uint64
	averageLatency uint64
	p50Latency     uint64
	p95Latency     uint64
	p99Latency     uint64
	pMaxLatency    uint64
}

func getAndReportReadMetrics(b *driver.B, output string) error {
	metrics, err := getMetrics("read", output)
	if err != nil {
		return err
	}
	return reportMetrics(b, "read", metrics)
}
func getAndReportWriteMetrics(b *driver.B, output string) error {
	metrics, err := getMetrics("write", output)
	if err != nil {
		return err
	}
	return reportMetrics(b, "write", metrics)
}

func getMetrics(metricType string, output string) (benchmarkMetrics, error) {
	re := regexp.MustCompile(fmt.Sprintf(`.*(__total)\n.*%s`, metricType))
	fmt.Printf("output: %s\n\n\n", output)
	match := re.FindString(output)
	if len(match) == 0 {
		return benchmarkMetrics{}, fmt.Errorf("failed to find %s metrics in output", metricType)
	}
	match = strings.Split(match, "\n")[1]
	fields := strings.Fields(match)

	stringToUint64 := func(field string) (uint64, error) {
		number, err := strconv.ParseFloat(field, 64)
		if err != nil {
			return 0, errors.Wrap(err, "Error parsing metrics to uint64")
		}
		return uint64(number), nil
	}

	fmt.Printf("fields: %+v\nlen:%d\n", fields, len(fields[2:]))
	uint64Fields := make([]uint64, len(fields[2:])-1)
	for i := range uint64Fields {
		var err error
		uint64Fields[i], err = stringToUint64(fields[2+i])
		if err != nil {
			return benchmarkMetrics{}, err
		}
	}

	metrics := benchmarkMetrics{
		totalOps:       uint64Fields[0],
		opsPerSecond:   uint64Fields[1],
		averageLatency: uint64Fields[2],
		p50Latency:     uint64Fields[3],
		p95Latency:     uint64Fields[4],
		p99Latency:     uint64Fields[5],
		pMaxLatency:    uint64Fields[6],
	}
	fmt.Printf("fields found:%+v\n", metrics)
	return metrics, nil
}

func reportMetrics(b *driver.B, metricType string, metrics benchmarkMetrics) error {
	b.Report(fmt.Sprintf("%s-ops-sec", metricType), metrics.opsPerSecond)
	b.Report(fmt.Sprintf("%s-ops-total", metricType), metrics.totalOps)
	b.Report(fmt.Sprintf("%s-avg-latency-ms", metricType), metrics.averageLatency)
	b.Report(fmt.Sprintf("%s-p50-latency-ms", metricType), metrics.p50Latency)
	b.Report(fmt.Sprintf("%s-p95-latency-ms", metricType), metrics.p95Latency)
	b.Report(fmt.Sprintf("%s-p99-latency-ms", metricType), metrics.p99Latency)
	b.Report(fmt.Sprintf("%s-pMax-latency-ms", metricType), metrics.pMaxLatency)
	return nil
}

func run(cfg *config) (err error) {
	var instances []*cockroachdbInstance
	// Launch the server.
	if cfg.bench.nodeCount == 1 {
		instances, err = launchSingleNodeCluster(cfg)
	} else {
		instances, err = launchCockroachCluster(cfg)
	}

	if err != nil {
		return fmt.Errorf("starting cluster: %v\n", err)
	}

	// Wait for the node to be ready, cockroach workload already handles retrying
	// in this case, but waiting will limit the amount of errors in the log.
	time.Sleep(waitTimeAfterStart)

	// Clean up the cluster after we're done.
	defer func() {
		for _, inst := range instances {
			if r := inst.shutdown(); r != nil {
				if err == nil {
					err = r
				} else {
					fmt.Fprintf(os.Stderr, "failed to shutdown %s: %v", inst.name, r)
				}
			}
			if err != nil && inst.output.Len() != 0 {
				fmt.Fprintf(os.Stderr, "=== Instance %q stdout+stderr ===\n", inst.name)
				fmt.Fprintln(os.Stderr, inst.output.String())
			}
		}
	}()

	// TODO(mknyszek): Consider collecting summed memory metrics for all instances.
	// TODO(mknyszek): Consider running all instances under perf.
	opts := []driver.RunOption{
		driver.DoPeakRSS(true),
		driver.DoPeakVM(true),
		driver.DoDefaultAvgRSS(),
		driver.DoCoreDump(true),
		driver.BenchmarkPID(instances[0].cmd.Process.Pid),
		driver.DoPerf(true),
	}
	return driver.RunBenchmark(cfg.bench.reportName, func(d *driver.B) error {
		// Set up diagnostics.
		var finishers []func() uint64
		if driver.DiagnosticEnabled(diagnostics.CPUProfile) {
			for _, inst := range instances {
				finishers = append(finishers, server.PollDiagnostic(
					inst.httpAddr(),
					cfg.tmpDir,
					cfg.bench.reportName,
					diagnostics.CPUProfile,
				))
			}
		}
		if driver.DiagnosticEnabled(diagnostics.Trace) {
			var sum atomic.Uint64
			for _, inst := range instances {
				stopTrace := server.PollDiagnostic(
					inst.httpAddr(),
					cfg.tmpDir,
					cfg.bench.reportName,
					diagnostics.Trace,
				)
				finishers = append(finishers, func() uint64 {
					n := stopTrace()
					sum.Add(n)
					return n
				})
			}
			defer func() {
				d.Report("trace-bytes", sum.Load())
			}()
		}
		if driver.DiagnosticEnabled(diagnostics.MemProfile) {
			for _, inst := range instances {
				inst := inst
				finishers = append(finishers, func() uint64 {
					n, err := server.CollectDiagnostic(
						inst.httpAddr(),
						cfg.tmpDir,
						cfg.bench.reportName,
						diagnostics.MemProfile,
					)
					if err != nil {
						fmt.Fprintf(os.Stderr, "failed to read memprofile: %v", err)
					}
					return uint64(n)
				})
			}
		}
		if len(finishers) != 0 {
			// Finish all the diagnostic collections in concurrently. Otherwise we could be waiting a while.
			defer func() {
				var wg sync.WaitGroup
				for _, finish := range finishers {
					finish := finish
					wg.Add(1)
					go func() {
						defer wg.Done()
						finish()
					}()
				}
				wg.Wait()
			}()
		}
		// Actually run the benchmark.
		return runBenchmark(d, cfg, instances)
	}, opts...)
}

func main() {
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "error: unexpected args\n")
		os.Exit(1)
	}
	for _, typ := range diagnostics.Types() {
		cliCfg.isProfiling = cliCfg.isProfiling || driver.DiagnosticEnabled(typ)
	}
	for i := range benchmarks {
		if benchmarks[i].name == cliCfg.benchName {
			cliCfg.bench = &benchmarks[i]
			break
		}
	}
	if cliCfg.bench == nil {
		fmt.Fprintf(os.Stderr, "error: unknown benchmark %q\n", cliCfg.benchName)
		os.Exit(1)
	}

	// We're going to launch a bunch of cockroachdb instances. Distribute
	// GOMAXPROCS between those and ourselves equally.
	procs := runtime.GOMAXPROCS(-1)
	procsPerInst := procs / (cliCfg.bench.nodeCount + 1)
	if procsPerInst == 0 {
		procsPerInst = 1
	}
	runtime.GOMAXPROCS(procsPerInst)
	cliCfg.procsPerInst = procsPerInst

	for i := 0; i < cliCfg.bench.nodeCount; i++ {

	}

	if err := run(&cliCfg); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
