// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package harnesses

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/benchmarks/sweet/common"
	"golang.org/x/benchmarks/sweet/common/log"
)

type CockroachDB struct{}

func (h CockroachDB) CheckPrerequisites() error {
	return nil
}

func (h CockroachDB) Get(gcfg *common.GetConfig) error {
	// Build against the latest stable release.
	return gitShallowClone(
		gcfg.SrcDir,
		"https://github.com/cockroachdb/cockroach",
		"release-24.1",
	)
}

func (h CockroachDB) Build(cfg *common.Config, bcfg *common.BuildConfig) error {
	// Build the cockroach binary.
	// We do this by using the cockroach `dev` tool. The dev tool is a bazel
	// wrapper normally used for building cockroach, but can also be to
	// generate artifacts that can then be built by `go build`.

	// Install bazel via bazelisk which is used by `dev`.
	if err := cfg.GoTool().Do("", "install", "github.com/bazelbuild/bazelisk@latest"); err != nil {
		return fmt.Errorf("error building bazelisk: %v", err)
	}

	// Configure the build env.
	env := cfg.BuildEnv.Env
	env = env.Prefix("PATH", filepath.Join(cfg.GoRoot, "bin")+":")
	env = env.MustSet("GOROOT=" + cfg.GoRoot)

	// Even though we aren't actually building anything with dev or bazel,
	// the dev tool expects this set or else it won't let us do anything
	// including generating the go files. Normally the `dev doctor` tool
	// does this for us, but it doesn't have write permissions out of the
	// box. It's easier to write it ourselves than to configure permissions
	// and let it potentially change anything else unexpectedly.
	bazelrc, err := os.Create(filepath.Join(bcfg.SrcDir, ".bazelrc.user"))
	if err != nil {
		return err
	}
	defer bazelrc.Close()
	setting := []byte("build --config=dev")
	_, err = bazelrc.Write(setting)
	if err != nil {
		return err
	}

	// Run dev doctor which will set up the environment for
	// the dev tool to work properly.
	cmd := exec.Command("./dev", "doctor")
	cmd.Dir = bcfg.SrcDir
	cmd.Env = env.Collapse()
	log.TraceCommand(cmd, false)

	// `dev doctor` is an interactive tool, pipe in input to
	// decline all of optional settings it asks us about.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdin.Write([]byte("n\nn\n\n"))
	if err = cmd.Run(); err != nil {
		return err
	}

	// Call `dev gen go` which will generate the Go code as well
	// as the c-deps needed.
	cmd = exec.Command("./dev", "gen", "go")
	cmd.Dir = bcfg.SrcDir
	cmd.Env = env.Collapse()
	log.TraceCommand(cmd, false)
	if err = cmd.Run(); err != nil {
		return err
	}
	// Finally build the cockroach binary with `go build`. Build the
	// cockroach-short binary as it is functionally the same, but
	// without the UI, making it much quicker to build.
	if err := cfg.GoTool().BuildPath(filepath.Join(bcfg.SrcDir, "pkg/cmd/cockroach-short"), bcfg.BinDir); err != nil {
		return err
	}

	// Rename the binary from cockroach-short to cockroach for
	// ease of use.
	if err := copyFile(filepath.Join(bcfg.BinDir, "cockroach"), filepath.Join(bcfg.BinDir, "cockroach-short")); err != nil {
		return err
	}

	// Build the benchmark wrapper.
	if err := cfg.GoTool().BuildPath(bcfg.BenchDir, filepath.Join(bcfg.BinDir, "cockroachdb-bench")); err != nil {
		return err
	}

	cmd = exec.Command("chmod", "-R", "755", filepath.Join(bcfg.BinDir, "cockroachdb-bench"))
	if err := cmd.Run(); err != nil {
		return err
	}

	// Clean up the bazel workspace. If we don't do this, our _bazel directory
	// will quickly grow as Bazel treats each run as its own workspace with its
	// own artifacts.
	cmd = exec.Command("bazel", "clean", "--expunge")
	cmd.Dir = bcfg.SrcDir
	if err := cmd.Run(); err != nil {
		return err
	}
	return nil
}

func (h CockroachDB) Run(cfg *common.Config, rcfg *common.RunConfig) error {
	for _, bench := range []string{"kv0/nodes=1", "kv50/nodes=1", "kv95/nodes=1", "kv0/nodes=3", "kv50/nodes=3", "kv95/nodes=3"} {
		args := append(rcfg.Args, []string{
			"-bench", bench,
			"-cockroachdb-bin", filepath.Join(rcfg.BinDir, "cockroach"),
			"-tmp", rcfg.TmpDir,
		}...)
		if rcfg.Short {
			args = append(args, "-short")
		}
		cmd := exec.Command(
			filepath.Join(rcfg.BinDir, "cockroachdb-bench"),
			args...,
		)
		cmd.Env = cfg.ExecEnv.Collapse()
		cmd.Stdout = rcfg.Results
		cmd.Stderr = rcfg.Results
		log.TraceCommand(cmd, false)
		if err := cmd.Run(); err != nil {
			return err
		}
		// Delete tmp because cockroachdb will have written something there and
		// might attempt to reuse it. We don't want to reuse the same cluster.
		if err := rmDirContents(rcfg.TmpDir); err != nil {
			return err
		}
	}
	return nil
}
