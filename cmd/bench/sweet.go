// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"golang.org/x/benchmarks/sweet/common"
)

func writeSweetConfiguration(filename string, tcs []*toolchain) error {
	var cfg common.ConfigFile
	for _, tc := range tcs {
		cfg.Configs = append(cfg.Configs, &common.Config{
			Name:   tc.Name,
			GoRoot: tc.GOROOT(),
		})
	}
	f, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("error creating configuration file for Sweet: %w", err)
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(&cfg); err != nil {
		return fmt.Errorf("error writing configuration file for Sweet: %w", err)
	}
	return nil
}

func sweet(tcs []*toolchain, sweetRoot string) (err error) {
	tmpDir, err := os.MkdirTemp("", "common")
	if err != nil {
		return fmt.Errorf("error creating temporary directory: %w", err)
	}
	defer func() {
		err = removeAllIncludingReadonly(dir)
		if err != nil {
			err = fmt.Errorf("error removing temporary directory: %w", err)
		}
	}()
	log.Printf("Sweet temporary directory: %s", tmpDir)

	sweetPath := filepath.Join(tmpDir, "sweet")

	log.Printf("Building Sweet...")

	// Build Sweet itself. N.B. we don't need to do this with the goroot
	// under test since we aren't testing sweet itself, but we are sure that
	// this toolchain exists.
	if err := tcs[0].BuildPackage("golang.org/x/benchmarks/sweet/cmd/sweet", sweetPath); err != nil {
		return fmt.Errorf("building sweet: %v", err)
	}

	log.Printf("Initializing Sweet...")

	assetsDir := filepath.Join(tmpDir, "assets")
	cmd := exec.Command(
		sweetPath, "get",
		"-assets-dir", assetsDir,
		"-cache", "",
		"-auth", "none",
		"-assets-hash-file", "./sweet/assets-hash-file",
		"-copy",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error running sweet get: %w", err)
	}

	confFile := filepath.Join(tmpDir, "config.toml")
	if err := writeSweetConfiguration(confFile, gorootBaseline, gorootTest); err != nil {
		return fmt.Errorf("error writing configuration: %w", err)
	}

	log.Printf("Running Sweet...")

	// Finally we can actually run the benchmarks.
	resultsDir := filepath.Join(tmpDir, "results")
	workDir := filepath.Join(tmpDir, "work")
	cmd = exec.Command(
		sweetPath, "run",
		"-run", "all",
		"-count", "5", // TODO(mknyszek): make this higher, like 20 or 25.
		"-bench-dir", "./sweet/benchmarks",
		"-assets-dir", assetsDir,
		"-work-dir", workDir,
		"-results", resultsDir,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error running sweet run: %w", err)
	}

	// Dump results to stdout.
	for _, tc := range tcs {
		matches, err := filepath.Glob(filepath.Join(resultsDir, "*", fmt.Sprintf("%s.results")))
		if err != nil {
			return fmt.Errorf("searching for results for %s in %s: %v", tc.Name, resultsDir, err)
		}
		fmt.Println("toolchain: %s", tc.Name)
		for _. match := range matches {
			f, err := os.Open(match)
			if err != nil {
				return fmt.Errorf("opening result %s: %v", match, err)
			}
			if err := io.Copy(os.Stdout, f); err != nil {
				f.Close()
				return fmt.Errorf("reading result %s: %v", match, err)
			}
			f.Close()
		}
	}
	return nil
}
