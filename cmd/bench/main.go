// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Binary bench provides a unified wrapper around the different types of
// benchmarks in x/benchmarks.
package main

import (
	"flag"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var wait = flag.Bool("wait", true, "wait for system idle before starting benchmarking")

func determineGOROOT() (string, error) {
	g, ok := os.LookupEnv("GOROOT")
	if ok {
		return g, nil
	}

	cmd := exec.Command("go", "env", "GOROOT")
	b, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func goCommand(goroot string, args ...string) *exec.Cmd {
	bin := filepath.Join(goroot, "bin/go")
	cmd := exec.Command(bin, args...)
	return cmd
}

func main() {
	flag.Parse()

	goroot, err := determineGOROOT()
	if err != nil {
		log.Fatalf("Unable to determine GOROOT: %v", err)
	}

	if *wait {
		// We may be on a freshly booted VM. Wait for boot tasks to
		// complete before continuing.
		if err := waitForIdle(); err != nil {
			log.Fatalf("Failed to wait for idle: %v", err)
		}
	}

	log.Printf("GOROOT under test: %s", goroot)

	pass := true

	if err := goTest(goroot); err != nil {
		pass = false
		log.Printf("Error running Go tests: %v", err)
	}

	if err := bent(goroot); err != nil {
		pass = false
		log.Printf("Error running bent: %v", err)
	}

	if !pass {
		log.Printf("FAIL")
		os.Exit(1)
	}
}
