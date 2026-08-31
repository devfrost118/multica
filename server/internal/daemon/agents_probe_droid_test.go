package daemon

import (
	"path/filepath"
	"runtime"
	"testing"
)

func stubEmptyLoginShellResolver(t *testing.T) {
	t.Helper()
	orig := resolveAgentsViaLoginShell
	t.Cleanup(func() { resolveAgentsViaLoginShell = orig })
	resolveAgentsViaLoginShell = func([]string) map[string]string {
		return map[string]string{}
	}
	resetShellResolveCacheForTest(t)
}

func writeFakeDroidCLI(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "droid")
	writeDaemonTestExecutable(t, path, []byte("#!/bin/sh\nexit 0\n"))
	return path
}

// TestProbeAgentCLIs_DiscoversDroidWhenCLIOnPATH is the PATH half of the
// FRO-275 regression: a host with a `droid` binary on PATH must surface it
// as a probe entry. Fork-local droid was dropped from probeAgentCLIs during
// the v0.4.32→v0.4.36 sync, so daemon-vm stayed offline even with a live CLI.
func TestProbeAgentCLIs_DiscoversDroidWhenCLIOnPATH(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}

	stubEmptyLoginShellResolver(t)
	fakeDir := t.TempDir()
	fakePath := writeFakeDroidCLI(t, fakeDir)

	t.Setenv("PATH", fakeDir)
	t.Setenv("MULTICA_DROID_PATH", "")
	t.Setenv("MULTICA_DROID_MODEL", "glm-5")

	entry, ok := probeAgentCLIs()["droid"]
	if !ok {
		t.Fatal("droid was not discovered by probeAgentCLIs with a CLI on PATH")
	}
	if entry.Command != "droid" {
		t.Errorf("droid command = %q, want %q", entry.Command, "droid")
	}
	if entry.Path != fakePath {
		t.Errorf("droid path = %q, want %q", entry.Path, fakePath)
	}
	if entry.Model != "glm-5" {
		t.Errorf("droid model = %q, want %q", entry.Model, "glm-5")
	}
}

// TestProbeAgentCLIs_DiscoversDroidViaPinnedPath is the env-var half: an
// operator-pinned MULTICA_DROID_PATH must be enough even when PATH cannot
// see a bare `droid` command. Env fixes on daemon-vm were a no-op because
// nothing read MULTICA_DROID_PATH.
func TestProbeAgentCLIs_DiscoversDroidViaPinnedPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}

	stubEmptyLoginShellResolver(t)
	fakePath := writeFakeDroidCLI(t, t.TempDir())

	t.Setenv("PATH", "")
	t.Setenv("MULTICA_DROID_PATH", fakePath)
	t.Setenv("MULTICA_DROID_MODEL", "")

	entry, ok := probeAgentCLIs()["droid"]
	if !ok {
		t.Fatal("droid was not discovered by probeAgentCLIs via MULTICA_DROID_PATH")
	}
	if entry.Command != fakePath {
		t.Errorf("droid command = %q, want pinned path %q", entry.Command, fakePath)
	}
	resolved, err := filepath.EvalSymlinks(fakePath)
	if err != nil {
		resolved = fakePath
	}
	if entry.Path != fakePath && entry.Path != resolved {
		t.Errorf("droid path = %q, want %q (or canonical %q)", entry.Path, fakePath, resolved)
	}
}

// TestProbeAgentCLIs_OmitsDroidWhenCLIMissing is the negative: no PATH hit,
// no MULTICA_DROID_PATH, no login-shell fallback → droid must stay absent.
func TestProbeAgentCLIs_OmitsDroidWhenCLIMissing(t *testing.T) {
	stubEmptyLoginShellResolver(t)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("MULTICA_DROID_PATH", "")

	if entry, ok := probeAgentCLIs()["droid"]; ok {
		t.Errorf("droid was discovered without a CLI or MULTICA_DROID_PATH, path=%q", entry.Path)
	}
}

// TestProbeAgentCLIs_DroidPinnedPathStaysHardMiss keeps a missing pinned
// MULTICA_DROID_PATH from silently resolving a different binary via PATH or
// the login shell — the same rule probe() applies to every other provider.
func TestProbeAgentCLIs_DroidPinnedPathStaysHardMiss(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell-script fixture is POSIX-only")
	}

	orig := resolveAgentsViaLoginShell
	t.Cleanup(func() { resolveAgentsViaLoginShell = orig })
	resolveAgentsViaLoginShell = func([]string) map[string]string {
		return map[string]string{"droid": "/fake/login-shell/droid"}
	}
	resetShellResolveCacheForTest(t)

	fakeDir := t.TempDir()
	writeFakeDroidCLI(t, fakeDir)

	t.Setenv("PATH", fakeDir)
	t.Setenv("MULTICA_DROID_PATH", "/nonexistent/pinned/droid")

	if entry, ok := probeAgentCLIs()["droid"]; ok {
		t.Errorf("pinned-but-missing MULTICA_DROID_PATH resolved to %q, want a hard miss", entry.Path)
	}
}

func TestProbeAgentCLIs_DroidResolvesViaLoginShell(t *testing.T) {
	orig := resolveAgentsViaLoginShell
	t.Cleanup(func() { resolveAgentsViaLoginShell = orig })
	resolveAgentsViaLoginShell = func([]string) map[string]string {
		return map[string]string{"droid": "/fake/local/bin/droid"}
	}
	resetShellResolveCacheForTest(t)

	t.Setenv("PATH", "")
	t.Setenv("MULTICA_DROID_PATH", "")

	entry, ok := probeAgentCLIs()["droid"]
	if !ok {
		t.Fatal("droid was not discovered via the login-shell fallback")
	}
	if entry.Path != "/fake/local/bin/droid" {
		t.Errorf("droid path = %q, want /fake/local/bin/droid", entry.Path)
	}
	if entry.Command != "droid" {
		t.Errorf("droid command = %q, want droid", entry.Command)
	}
}

// TestDefaultAgentCommandNamesIncludesDroid guards the other half of FRO-275:
// cachedShellResolvedAgents only asks the login shell about names in
// defaultAgentCommandNames, so omitting "droid" leaves the fallback blind
// even once probeAgentCLIs consults it.
func TestDefaultAgentCommandNamesIncludesDroid(t *testing.T) {
	for _, name := range defaultAgentCommandNames {
		if name == "droid" {
			return
		}
	}
	t.Fatal("defaultAgentCommandNames is missing \"droid\"; the login-shell resolver " +
		"only pre-fetches names in that list, so droid stays undetectable on a GUI-launched daemon")
}
