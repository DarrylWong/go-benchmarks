// Copyright 2021 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"golang.org/x/benchmarks/sweet/common"
	"golang.org/x/sync/semaphore"
)

func TestSweetEndToEnd(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("Sweet is currently only fully supported on linux/amd64")
	}
	if testing.Short() {
		t.Skip("the full Sweet end-to-end experience takes several minutes")
	}
	goRoot := os.Getenv("GOROOT")
	if goRoot == "" {
		data, err := exec.Command("go", "env", "GOROOT").Output()
		if err != nil {
			t.Fatalf("failed to find a GOROOT: %v", err)
		}
		goRoot = strings.TrimSpace(string(data))
	}
	goTool := &common.Go{
		Tool: filepath.Join(goRoot, "bin", "go"),
		Env:  common.NewEnvFromEnviron(),
	}

	// Build sweet.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	sweetRoot := filepath.Dir(filepath.Dir(wd))
	sweetBin := filepath.Join(sweetRoot, "sweet")
	if err := goTool.BuildPath(filepath.Join(sweetRoot, "cmd", "sweet"), sweetBin); err != nil {
		t.Fatal(err)
	}
	// We're on a builder, so arrange all this a little differently.
	// Let's do all our work in the work directory which has a lot
	// more headroom, and put the compressed assets in /tmp.
	var tmpDir, assetsCacheDir string
	if os.Getenv("GO_BUILDER_NAME") != "" {
		tmpDir = filepath.Join(sweetRoot, "tmp")
		if err := os.Mkdir(tmpDir, 0777); err != nil {
			t.Fatal(err)
		}
		// Be explicit that we want /tmp, because the builder is
		// going to try and give us /workdir/tmp which will not
		// have enough space for us.
		assetsCacheDir := filepath.Join("/", "tmp", "go-sweet-assets")
	} else {
		tmpDir, err = os.MkdirTemp("", "go-sweet-test")
		if err != nil {
			t.Fatal(err)
		}
		assetsCacheDir = filepath.Join(tmpDir, "assets")
	}

	// Download assets.
	getCmd := exec.Command(sweetBin, "get",
		"-auth", "none",
		"-cache", assetsCacheDir, // Make a full copy so we can mutate it.
		"-assets-hash-file", filepath.Join(sweetRoot, "assets.hash"),
	)
	if output, err := getCmd.CombinedOutput(); err != nil {
		t.Logf("command output:\n%s", string(output))
		t.Fatal(err)
	}

	// TODO(mknyszek): Test regenerating assets. As it stands, the following
	// parts of the test will fail if the source assets change, since they're
	// prebuilt and baked into the assets archive. The only recourse is to
	// first upload the new archive with the prebuilt assets (i.e. run sweet
	// gen locally), bump the version, and then upload it (i.e. sweet put).

	// Run each benchmark once.
	benchDir := filepath.Join(sweetRoot, "benchmarks")
	cfgPath := makeConfigFile(t, goRoot)

	var outputMu sync.Mutex
	runShard := func(shard, resultsDir, workDir string) {
		runCmd := exec.Command(sweetBin, "run",
			"-run", shard,
			"-shell",
			"-count", "1",
			"-cache", assetsCacheDir,
			"-bench-dir", benchDir,
			"-results", resultsDir,
			"-work-dir", workDir,
			"-short",
			cfgPath,
		)
		output, runErr := runCmd.CombinedOutput()

		outputMu.Lock()
		defer outputMu.Unlock()

		// Poke at the results directory.
		matches, err := filepath.Glob(filepath.Join(resultsDir, "*", "go.results"))
		if err != nil {
			t.Errorf("failed to search results directory for results: %v", err)
		}
		if len(matches) == 0 {
			t.Log("no results produced.")
		}

		// Dump additional information in case of error, and
		// check for reasonable results in the case of no error.
		for _, match := range matches {
			benchmark := filepath.Base(filepath.Dir(match))
			if runErr != nil {
				t.Logf("output for %s:", benchmark)
			}
			data, err := os.ReadFile(match)
			if err != nil {
				t.Errorf("failed to read results for %si: %v", benchmark, err)
				continue
			}
			if runErr != nil {
				t.Log(string(data))
				continue
			}
			// TODO(mknyszek): Check to make sure the results look reasonable.
		}
		if runErr != nil {
			t.Logf("command output:\n%s", string(output))
			t.Error(runErr)
		}
	}
	// Limit parallelism to conserve memory.
	sema := semaphore.NewWeighted(2)
	for i, shard := range []string{
		/*"tile38",*/ "go-build", "biogo-igor", "biogo-krishna", "bleve-query",
		"gvisor", "fogleman-pt", "bleve-index,fogleman-fauxgl,gopher-lua,markdown",
	} {
		sema.Acquire(context.Background(), 1)
		go func(i int, shard string) {
			defer sema.Release(1)
			resultsDir := filepath.Join(tmpDir, fmt.Sprintf("results-%d", i))
			workDir := filepath.Join(tmpDir, fmt.Sprintf("tmp-%d", i))
			runShard(shard, resultsDir, workDir)
		}(i, shard)
	}
}

func makeConfigFile(t *testing.T, goRoot string) string {
	t.Helper()

	f, err := os.CreateTemp("", "config.toml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg := common.ConfigFile{
		Configs: []*common.Config{
			&common.Config{
				Name:   "go",
				GoRoot: goRoot,
			},
		},
	}
	b, err := common.ConfigFileMarshalTOML(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(b); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}
