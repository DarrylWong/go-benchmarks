package main_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

const dataDir = "testdata"

var binary, dir string

// We implement TestMain so remove the test binary when all is done.
func TestMain(m *testing.M) {
	os.Exit(testMain(m))
}

func testMain(m *testing.M) int {
	var err error
	dir, err = os.MkdirTemp("", "vet_test")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)
	binary = filepath.Join(dir, "testvet.exe")
	return m.Run()
}

var (
	buildMu sync.Mutex // guards following
	built   = false    // We have built the binary.
	failed  = false    // We have failed to build the binary, don't try again.
)

func Build(t *testing.T) {
	buildMu.Lock()
	defer buildMu.Unlock()
	if built {
		return
	}
	if failed {
		t.Skip("cannot run on this environment")
	}
	cmd := exec.Command("go", "build", "-o", binary)
	output, err := cmd.CombinedOutput()
	if err != nil {
		failed = true
		fmt.Fprintf(os.Stderr, "%s\n", output)
		t.Fatal(err)
	}
	built = true
}

func TestBent(t *testing.T) {
	Build(t)
	cmd := exec.Command(binary, "-I")
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		failed = true
		fmt.Fprintf(os.Stderr, "%s\n", output)
		t.Fatal(err)
	}
	t.Log(string(output))
	cmd = exec.Command(binary, "-l", "-C=configurations-sample.toml")
	cmd.Dir = dir
	output, err = cmd.CombinedOutput()
	if err != nil {
		failed = true
		fmt.Fprintf(os.Stderr, "%s\n", output)
		t.Fatal(err)
	}
	t.Log(string(output))
}
