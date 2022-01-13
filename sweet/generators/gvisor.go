// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package generators

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/benchmarks/sweet/common"
	"golang.org/x/benchmarks/sweet/common/fileutil"
	"golang.org/x/benchmarks/sweet/harnesses"

	osi "github.com/opencontainers/runtime-spec/specs-go"
)

// GVisor is a dynamic assets Generator for the gvisor benchmark.
type GVisor struct{}

// Generate builds binaries for workloads that will run under gVisor
// as part of the benchmark. The sources for these workloads live in
// the source assets directory and are relatively short Go programs.
//
// It also copies over static assets which are necessary to run the
// benchmarks.
func (_ GVisor) Generate(cfg *common.GenConfig) error {
	goTool := *cfg.GoTool
	goTool.Env = goTool.Env.MustSet("CGO_ENABLED=0") // Disable CGO for workloads.

	// Build workload sources into binaries in the output directory,
	// with one binary for each supported platform.
	workloads := []string{
		"http",
		"syscall",
	}
	for _, workload := range workloads {
		workloadSrcDir := filepath.Join(cfg.SourceAssetsDir, workload)
		workloadOutDir := filepath.Join(cfg.OutputDir, workload)
		if err := os.MkdirAll(workloadOutDir, 0755); err != nil {
			return err
		}
		for _, p := range common.SupportedPlatforms {
			// Generate the output directory.
			platformDirName := fmt.Sprintf("%s-%s", p.GOOS, p.GOARCH)
			workloadBinOutDir := filepath.Join(workloadOutDir, "bin", platformDirName)
			if err := os.MkdirAll(workloadBinOutDir, 0755); err != nil {
				return err
			}
			goTool := common.Go{Tool: goTool.Tool, Env: p.BuildEnv(goTool.Env)}

			// Build the workload.
			err := goTool.BuildPath(workloadSrcDir, filepath.Join(workloadBinOutDir, "workload"))
			if err != nil {
				return fmt.Errorf("building workload %s for %s: %v", workload, p, err)
			}
		}
	}

	// In order to regenerate startup/config.json, we require a working
	// copy of runsc. Get and build it from the harness.
	//
	// Create a temporary directory where we can put the gVisor source.
	tmpDir, err := ioutil.TempDir("", "gvisor-gen")
	if err != nil {
		return err
	}
	srcDir := filepath.Join(tmpDir, "src")
	if err := os.MkdirAll(srcDir, os.ModePerm); err != nil {
		return err
	}
	if err := (harnesses.GVisor{}).Get(srcDir); err != nil {
		return err
	}

	// Ensure the startup subdirectory exists.
	if err := os.MkdirAll(filepath.Join(cfg.OutputDir, "startup"), 0755); err != nil {
		return err
	}

	// Build the runsc package in the repository. CGO_ENABLED must be 0.
	// See https://github.com/google/gvisor#using-go-get.
	cfg.GoTool.Env = cfg.GoTool.Env.MustSet("CGO_ENABLED=0")
	runscBin := filepath.Join(tmpDir, "runsc")
	if err := cfg.GoTool.BuildPath(filepath.Join(srcDir, "runsc"), runscBin); err != nil {
		return err
	}

	// Delete config.json if it already exists, because runsc
	// will fail otherwise.
	specFile := filepath.Join(cfg.OutputDir, "startup", "config.json")
	if err := os.Remove(specFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}

	// Generate config.json.
	cmd := exec.Command(runscBin, "spec")
	cmd.Dir = filepath.Join(cfg.OutputDir, "startup")
	if err := cmd.Run(); err != nil {
		return err
	}

	// Mutate the config.json slightly for our purposes and write it back out.
	specBytes, err := os.ReadFile(specFile)
	if err != nil {
		return err
	}
	var spec osi.Spec
	if err := json.Unmarshal(specBytes, &spec); err != nil {
		return err
	}
	spec.Process.Terminal = false
	spec.Process.Args = []string{"/hello"}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "    ")
	if err := enc.Encode(&spec); err != nil {
		return err
	}
	if err := os.WriteFile(specFile, buf.Bytes(), 0666); err != nil {
		return err
	}

	// Everything below this point is static assets. If we're in the
	// same directory, just stop here.
	if cfg.AssetsDir == cfg.OutputDir {
		return nil
	}

	// Generate additional directory structure for static assets
	// that isn't already generated by the build process above.
	if err := os.MkdirAll(filepath.Join(cfg.OutputDir, "http", "assets"), 0755); err != nil {
		return err
	}

	// Copy static assets over.
	staticAssets := []string{
		filepath.Join("http", "assets", "gopherhat.jpg"),
		filepath.Join("http", "assets", "gophermega.jpg"),
		filepath.Join("http", "assets", "gopherswim.jpg"),
		filepath.Join("http", "assets", "gopherhelmet.jpg"),
		filepath.Join("http", "assets", "gopherrunning.jpg"),
		filepath.Join("http", "assets", "gopherswrench.jpg"),
		filepath.Join("http", "README.md"),
		filepath.Join("startup", "README.md"),
		filepath.Join("syscall", "README.md"),
	}
	if err := copyFiles(cfg.OutputDir, cfg.AssetsDir, staticAssets); err != nil {
		return err
	}

	// As a special case, copy everything under startup/rootfs.
	// It's a rootfs, so enumerating everything here would be tedious
	// and not really useful.
	//
	// TODO(mknyszek): Generate this directory from a container image.
	// There's some complications to this, because Cloud Build runs
	// inside docker, and this is generated from a docker image.
	return fileutil.CopyDir(
		filepath.Join(cfg.OutputDir, "startup", "rootfs"),
		filepath.Join(cfg.AssetsDir, "startup", "rootfs"),
		nil,
	)
}