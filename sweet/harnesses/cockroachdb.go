// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package harnesses

import (
	"fmt"
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
	//// Install Bazelisk
	//bzlCmd := exec.Command("go install github.com/bazelbuild/bazelisk@latest")
	//if err := bzlCmd.Run(); err != nil {
	//	return err
	//}
	//// Build against the latest stable release.
	//return gitShallowClone(
	//	gcfg.SrcDir,
	//	"https://github.com/cockroachdb/cockroach",
	//	"v23.2.3",
	//)
	return nil
}

func (h CockroachDB) Build(cfg *common.Config, bcfg *common.BuildConfig) error {
	//env := cfg.BuildEnv.Env

	// Add the Go tool to PATH, since etcd's Makefile doesn't provide enough
	// visibility into how etcd is built to allow us to pass this information
	// directly. Also set the GOROOT explicitly because it might have propagated
	// differently from the environment.
	//env = env.Prefix("PATH", filepath.Join(cfg.GoRoot, "bin")+":")
	//env = env.MustSet("GOROOT=" + cfg.GoRoot)
	//
	////--@io_bazel_rules_go//go/toolchain:sdk_version="host"
	//cmd := exec.Command("bazel build", "-C", bcfg.SrcDir, "build")
	//cmd.Env = env.Collapse()
	//log.TraceCommand(cmd, false)
	//// Call Output here to get an *ExitError with a populated Stderr field.
	//if _, err := cmd.Output(); err != nil {
	//	return err
	//}
	// Note that no matter what we do, the build script insists on putting the
	// binaries into the source directory, so copy the one we care about into
	// BinDir.
	if err := copyFile(filepath.Join(bcfg.BinDir, "cockroach"), "./cockroach"); err != nil {
		return err
	}
	//// Build etcd's benchmarking tool. Our benchmark is just a wrapper around that.
	//benchmarkPkg := filepath.Join(bcfg.SrcDir, "tools", "benchmark")
	//if err := cfg.GoTool().BuildPath(benchmarkPkg, filepath.Join(bcfg.BinDir, "benchmark")); err != nil {
	//	return err
	//}
	//// Build the benchmark wrapper.
	if err := cfg.GoTool().BuildPath(bcfg.BenchDir, filepath.Join(bcfg.BinDir, "cockroachdb-bench")); err != nil {
		return err
	}
	cmd := exec.Command("ls")
	//if err := cmd.Run(); err != nil {
	//	fmt.Printf("darryl: error=%s\n", err.Error())
	//	return err
	//}
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Printf("Output: %s, error: %s\n", string(output[:]), err.Error())
		return err
	}

	cmd = exec.Command("chmod", "-R", "755", filepath.Join(bcfg.BinDir, "cockroachdb-bench"))
	//if err := cmd.Run(); err != nil {
	//	fmt.Printf("darryl: error=%s\n", err.Error())
	//	return err
	//}
	if output, err := cmd.CombinedOutput(); err != nil {
		fmt.Printf("Output: %s, error: %s\n", string(output[:]), err.Error())
		return err
	}
	fmt.Println("darryl: E")
	return nil
}

func (h CockroachDB) Run(cfg *common.Config, rcfg *common.RunConfig) error {
	for _, bench := range []string{"kv95/nodes=3"} { //"kv95/nodes=1",
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
		// might attempt to reuse it.
		if err := rmDirContents(rcfg.TmpDir); err != nil {
			return err
		}
	}
	return nil
}
