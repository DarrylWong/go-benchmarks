// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"log"
	"os"
	"os/exec"
)

func goTest(goroot string) error {
	log.Printf("Running Go test benchmarks for GOROOT %s", goroot)

	args := []string{
		"test",
		"-v",
		"-run=none",
		"-bench=.",
		"-count=5",
		"golang.org/x/benchmarks/...",
	}
	cmd := exec.Command(goBinary(goroot), args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = []string{
		"GOROOT=" + goroot,
		"HOME=" + os.Getenv("HOME"),
		"PATH=" + os.Getenv("PATH"),
	}
	return cmd.Run()
}
