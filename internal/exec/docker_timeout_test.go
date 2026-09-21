package exec

// H5 tests: Docker lifecycle deadlines. Verifies that the dockerCmd and
// dockerCmdRun helpers fail within their deadline with an actionable error
// message when the docker daemon is wedged (simulated by a fake docker
// binary that sleeps), and succeed quickly when docker responds normally.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// writeFakeDocker creates a fake `docker` script in a temp directory that
// sleeps for the given duration before producing any output. The script is
// executable and the directory is returned so the caller can prepend it to
// PATH.
func writeFakeDocker(t *testing.T, sleepSec int) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\nsleep " + strconv.Itoa(sleepSec) + "\n"
	path := filepath.Join(dir, "docker")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestDockerCmd_Timeout verifies that dockerCmd fails within the deadline
// with an actionable error message mentioning the timeout.
func TestDockerCmd_Timeout(t *testing.T) {
	dir := writeFakeDocker(t, 30) // sleeps 30s
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	start := time.Now()
	_, timedOut, err := dockerCmd(1*time.Second, "info")
	elapsed := time.Since(start)

	if !timedOut {
		t.Error("dockerCmd should report timedOut=true when deadline exceeds")
	}
	if err == nil {
		t.Error("dockerCmd should return an error on timeout")
	}
	if elapsed > 5*time.Second {
		t.Errorf("dockerCmd took %s, expected <5s (deadline was 1s + 2s WaitDelay)", elapsed)
	}
	if err != nil && !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error should mention timeout: %v", err)
	}
}

// TestDockerCmdRun_Timeout verifies that dockerCmdRun also fails within the deadline.
func TestDockerCmdRun_Timeout(t *testing.T) {
	dir := writeFakeDocker(t, 30)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	start := time.Now()
	timedOut, err := dockerCmdRun(1*time.Second, "rm", "-f", "test")
	elapsed := time.Since(start)

	if !timedOut {
		t.Error("dockerCmdRun should report timedOut=true when deadline exceeds")
	}
	if err == nil {
		t.Error("dockerCmdRun should return an error on timeout")
	}
	if elapsed > 5*time.Second {
		t.Errorf("dockerCmdRun took %s, expected <5s", elapsed)
	}
}

// TestDockerCmd_QuickSuccess verifies that dockerCmd succeeds quickly when
// docker responds normally (the fake script exits immediately with sleep 0).
func TestDockerCmd_QuickSuccess(t *testing.T) {
	dir := writeFakeDocker(t, 0) // sleeps 0s (immediate)
	t.Setenv("PATH", dir+":"+os.Getenv("PATH"))

	_, timedOut, err := dockerCmd(5*time.Second, "info")
	if timedOut {
		t.Error("dockerCmd should not time out when docker responds quickly")
	}
	if err != nil {
		t.Errorf("dockerCmd should succeed when docker responds: %v", err)
	}
}
