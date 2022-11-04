// Copyright 2022 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
)

func Create(ctx context.Context, logger io.Writer, typ string) (string, error) {
	cmd := exec.CommandContext(ctx, "gomote", "create", typ)
	cmd.Stderr = logger

	result, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(result)), nil
}

func Push(ctx context.Context, logger io.Writer, inst, goroot string) error {
	cmd := exec.CommandContext(ctx, "gomote", "push", inst)
	cmd.Stdout = logger
	cmd.Stderr = logger
	cmd.Env = append(cmd.Environ(), "GOROOT="+goroot)

	fmt.Fprintf(logger, "Running %v\n", cmd.Args)
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func Destroy(ctx context.Context, logger io.Writer, inst string) error {
	cmd := exec.CommandContext(ctx, "gomote", "destroy", inst)
	cmd.Stdout = logger
	cmd.Stderr = logger

	fmt.Fprintf(logger, "Running %v\n", cmd.Args)
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func Run(ctx context.Context, logger io.Writer, inst string, remoteCmd ...string) error {
	args := []string{"run"}
	args = append(args, inst)
	args = append(args, remoteCmd...)
	cmd := exec.CommandContext(ctx, "gomote", args...)
	cmd.Stdout = logger
	cmd.Stderr = logger

	fmt.Fprintf(logger, "Running %v\n", cmd.Args)
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

// gomote run times out after 2hr, so hack around that by using SSH.
func RunViaSSH(ctx context.Context, logger io.Writer, inst string, remoteCmd string) error {
	cmd := exec.CommandContext(ctx, "gomote", "ssh", inst)
	cmd.Stderr = logger

	result, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("error running gomote ssh for ssh command: %w", err)
	}

	// First line is SSH command. e.g., `$ /usr/bin/ssh -o ...`
	sshCLI := strings.SplitN(string(result), "\n", 2)[0]
	if len(sshCLI) < 2 {
		return fmt.Errorf("first line isn't command: %q", sshCLI)
	}
	sshCLI = sshCLI[2:]
	fmt.Fprintf(logger, "Got SSH command: %q\n", sshCLI)

	args := strings.Fields(sshCLI)
	// ssh -t -t forces PTY allocation even though stdin isn't a PTY. This
	// is required because the coordinator SSH server doesn't support
	// running commands, it requires a PTY. So we fake it.
	args = append(args, "-t", "-t")

	cmd = exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Stdout = logger
	cmd.Stderr = logger

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("error creating stdin pipe: %w", err)
	}
	defer stdin.Close()

	fmt.Fprintf(logger, "Running %v\n", cmd.Args)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("error starting ssh: %w", err)
	}

	fmt.Fprintln(stdin, "TMPDIR=/workdir/tmp")
	fmt.Fprintln(stdin, "GOCACHE=/workdir/gocache")
	fmt.Fprintln(stdin, "export TMPDIR GOCACHE")
	fmt.Fprintln(stdin, remoteCmd)
	fmt.Fprintln(stdin, "exit") // since ssh thinks this is a PTY, explicitly ask the shell to exit.

	return cmd.Wait()
}

func run(cdfThresh float64, budget int) error {
	ctx := context.Background()

	name := fmt.Sprintf("cdf%f-budget%d.log", cdfThresh, budget)
	f, err := os.Create(name)
	if err != nil {
		return fmt.Errorf("error creating log file: %w", err)
	}
	defer f.Close()
	logger := f

	var inst string
	for {
		inst, err = Create(ctx, logger, "linux-amd64-perf")
		if err != nil {
			log.Printf("error creating instance: %v", err)
			continue
		}
		break
	}
	defer Destroy(ctx, logger, inst)

	log.Printf("%s running on %s", name, inst)

	if err := Run(ctx, logger, inst, "/bin/mount", "-o", "remount,size=30G", "/workdir"); err != nil {
		return fmt.Errorf("error remounting tmpfs on %s: %w", inst, err)
	}

	goroot := "/usr/local/google/home/mpratt/src/go"
	if err := Push(ctx, logger, inst, goroot); err != nil {
		return fmt.Errorf("error pushing to %s: %w", inst, err)
	}

	if err := Run(ctx, logger, inst, "./go/src/make.bash"); err != nil {
		return fmt.Errorf("error building Go on %s: %w", inst, err)
	}

	// GOPATH must be in /workdir, otherwise the root partition will run out of space.
	if err := Run(ctx, logger, inst, "/workdir/go/bin/go", "env", "-w", "GOPATH=/workdir/gopath"); err != nil {
		return fmt.Errorf("error setting GOPATH on %s: %w", inst, err)
	}

	if err := Run(ctx, logger, inst, "/bin/bash", "-c", "git clone https://go.googlesource.com/benchmarks /workdir/benchmarks && cd /workdir/benchmarks && /workdir/go/bin/go build -o /workdir/sweet golang.org/x/benchmarks/sweet/cmd/sweet"); err != nil {
		return fmt.Errorf("error building sweet on %s: %w", inst, err)
	}

	if err := Run(ctx, logger, inst, "/bin/bash", "-c", "git clone https://go.googlesource.com/perf /workdir/perf && cd /workdir/perf && git fetch https://go.googlesource.com/perf refs/changes/69/309969/11 && git checkout FETCH_HEAD && /workdir/go/bin/go build -o /workdir/benchstat golang.org/x/perf/cmd/benchstat"); err != nil {
		return fmt.Errorf("error building benchstat on %s: %w", inst, err)
	}

	if err := Run(ctx, logger, inst, "/bin/bash", "-c", fmt.Sprintf(`cat >/workdir/config.toml <<EOF
[[config]]
	name = "experiment"
	goroot = "/workdir/go"
	envbuild = ["GOFLAGS=-gcflags=all=-d=inlinehotcallsitecdfthreshold=%f,inlinehotbudget=%d"]
EOF`, cdfThresh, budget)); err != nil {
		return fmt.Errorf("error building writing config on %s: %w", inst, err)
	}

	if err := Run(ctx, logger, inst, "/bin/bash", "-c", "/workdir/sweet get -assets-hash-file=/workdir/benchmarks/sweet/assets.hash -cache=/tmp/go-sweet-assets"); err != nil {
		return fmt.Errorf("error fetching sweet assests on %s: %w", inst, err)
	}

	log.Printf("%s running sweet", name)
	if err := RunViaSSH(ctx, logger, inst, `/bin/bash -c "/workdir/sweet run -pgo -work-dir /workdir/work -results /workdir/results -cache /tmp/go-sweet-assets -bench-dir /workdir/benchmarks/sweet/benchmarks -count 10 -run all /workdir/config.toml"`); err != nil {
		return fmt.Errorf("error running sweet on %s: %w", inst, err)
	}

	if err := Run(ctx, logger, inst, "/bin/bash", "-c", "cat /workdir/results/*/experiment.results > /workdir/nopgo.txt && cat /workdir/results/*/experiment.pgo.results > /workdir/pgo.txt"); err != nil {
		return fmt.Errorf("error grouping results on %s: %w", inst, err)
	}

	if err := Run(ctx, logger, inst, "/bin/bash", "-c", "echo nopgo && cat /workdir/nopgo.txt && echo pgo && cat /workdir/pgo.txt"); err != nil {
		return fmt.Errorf("error dumping results on %s: %w", inst, err)
	}

	if err := Run(ctx, logger, inst, "/workdir/benchstat", "/workdir/nopgo.txt", "/workdir/pgo.txt"); err != nil {
		return fmt.Errorf("error running benchstat on %s: %w", inst, err)
	}

	return nil
}

func main() {
	cdfThresh := []float64{90, 95, 99, 99.9}
	budget := []int{160, 1000, 2000, 4000}

	var wg sync.WaitGroup
	for _, c := range cdfThresh {
		for _, b := range budget {
			c := c
			b := b

			wg.Add(1)
			go func() {
				defer wg.Done()

				if err := run(c, b); err != nil {
					log.Printf("error running c=%f, b=%d: %v", c, b, err)
				}
			}()
		}
	}

	wg.Wait()
}
