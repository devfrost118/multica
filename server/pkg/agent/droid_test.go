package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNewReturnsDroidBackend(t *testing.T) {
	t.Parallel()
	b, err := New("droid", Config{ExecutablePath: "/nonexistent/droid"})
	if err != nil {
		t.Fatalf("New(droid) error: %v", err)
	}
	if _, ok := b.(*droidBackend); !ok {
		t.Fatalf("expected *droidBackend, got %T", b)
	}
}

func fakeDroidScript() string {
	if runtime.GOOS != "windows" {
		return `#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--append-system-prompt-file" ]; then
    printf '%s\n' "$1" >> "$DROID_ARGS_FILE"
    shift
    cat "$1" > "$DROID_SYSTEM_PROMPT_FILE_CONTENT"
  fi
  printf '%s\n' "$1" >> "$DROID_ARGS_FILE"
  shift
done
printf '{"type":"completion","finalText":"ok"}\n'
`
	}

	return `@echo off
chcp 65001 >nul
:loop
if "%~1"=="" goto done
if "%~1"=="--append-system-prompt-file" (
  >>"%DROID_ARGS_FILE%" echo %~1
  type "%~2" > "%DROID_SYSTEM_PROMPT_FILE_CONTENT%"
  >>"%DROID_ARGS_FILE%" echo %~2
  shift
  shift
  goto loop
)
>>"%DROID_ARGS_FILE%" echo %~1
shift
goto loop
:done
echo {"type":"completion","finalText":"ok"}
`
}

func TestDroidBackendPassesLargeSystemPromptThroughFile(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	argsFile := filepath.Join(tempDir, "argv.txt")
	promptContentFile := filepath.Join(tempDir, "system-prompt.txt")
	fakeName := "droid"
	if runtime.GOOS == "windows" {
		fakeName += ".cmd"
	}
	fakePath := filepath.Join(tempDir, fakeName)
	writeTestExecutable(t, fakePath, []byte(fakeDroidScript()))

	workDir := t.TempDir()
	runtimeBrief := strings.Repeat("runtime brief ", 3_000)
	if len(runtimeBrief) <= 32_767 {
		t.Fatalf("runtime brief must exceed the Windows argv limit, got %d bytes", len(runtimeBrief))
	}

	backend, err := New("droid", Config{
		ExecutablePath: fakePath,
		Logger:         slog.Default(),
		Env: map[string]string{
			"DROID_ARGS_FILE":                  argsFile,
			"DROID_SYSTEM_PROMPT_FILE_CONTENT": promptContentFile,
		},
	})
	if err != nil {
		t.Fatalf("new droid backend: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := backend.Execute(ctx, "inspect the repository", ExecOptions{
		Cwd:          workDir,
		SystemPrompt: runtimeBrief,
		Timeout:      5 * time.Second,
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()
	if result := <-session.Result; result.Status != "completed" {
		t.Fatalf("result status = %q, error = %q; want completed", result.Status, result.Error)
	}

	rawArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("read argv capture: %v", err)
	}
	args := splitNonEmptyLines(string(rawArgs))
	for index, arg := range args {
		args[index] = strings.TrimSpace(arg)
	}
	promptFlagIndex := argIndexOf(args, "--append-system-prompt-file")
	if promptFlagIndex == -1 || promptFlagIndex == len(args)-1 {
		t.Fatalf("expected --append-system-prompt-file <path> in argv, got %v", args)
	}
	promptPath := args[promptFlagIndex+1]
	if filepath.Dir(promptPath) != workDir {
		t.Errorf("system prompt file must be task-local: got %q, workdir %q", promptPath, workDir)
	}
	if filepath.Base(promptPath) == "AGENTS.md" || filepath.Base(promptPath) == "CLAUDE.md" {
		t.Errorf("system prompt file must not overwrite managed config: got %q", promptPath)
	}
	for _, arg := range args {
		if strings.Contains(arg, runtimeBrief) {
			t.Errorf("runtime brief must not appear in argv: %q", arg)
		}
	}
	if got := args[len(args)-1]; got != "inspect the repository" {
		t.Errorf("final positional prompt = %q, want user prompt", got)
	}

	gotPrompt, err := os.ReadFile(promptContentFile)
	if err != nil {
		t.Fatalf("read captured system prompt: %v", err)
	}
	if string(gotPrompt) != runtimeBrief {
		t.Errorf("system prompt file content differs from runtime brief")
	}
	if _, err := os.Stat(promptPath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temporary system prompt file should be removed after execution, stat err = %v", err)
	}
}

func TestNormalizeDroidModelID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in   string
		want string
	}{
		// Already in droid format — pass-through.
		{"claude-opus-4-7", "claude-opus-4-7"},
		{"claude-sonnet-4-6", "claude-sonnet-4-6"},
		{"gpt-5.5", "gpt-5.5"},
		{"kimi-k2.6", "kimi-k2.6"},
		// Multica shared-catalog form: provider/dot-version.
		{"anthropic/claude-sonnet-4.6", "claude-sonnet-4-6"},
		{"anthropic/claude-opus-4.7", "claude-opus-4-7"},
		// Prefix strip without dot conversion needed.
		{"openai/gpt-5.5", "gpt-5.5"},
		// Dot-form claude id without provider prefix.
		{"claude-haiku-4.5", "claude-haiku-4.5"}, // not in catalog — pass through; UI input was wrong
		// Unknown — pass through so droid surfaces its real error.
		{"some-future-model-id", "some-future-model-id"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeDroidModelID(tt.in)
		if got != tt.want {
			t.Errorf("normalizeDroidModelID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func hangingDroidScript() string {
	if runtime.GOOS != "windows" {
		return `#!/bin/sh
while :; do sleep 1; done
`
	}
	return `@echo off
:loop
ping -n 2 127.0.0.1 >nul
goto loop
`
}

func newHangingDroidBackend(t *testing.T) Backend {
	t.Helper()
	fakeName := "droid"
	if runtime.GOOS == "windows" {
		fakeName += ".cmd"
	}
	fakePath := filepath.Join(t.TempDir(), fakeName)
	writeTestExecutable(t, fakePath, []byte(hangingDroidScript()))
	backend, err := New("droid", Config{ExecutablePath: fakePath, Logger: slog.Default()})
	if err != nil {
		t.Fatalf("new hanging droid backend: %v", err)
	}
	return backend
}

func awaitDroidResult(t *testing.T, session *Session, bound time.Duration) Result {
	t.Helper()
	go func() {
		for range session.Messages {
		}
	}()
	select {
	case result, ok := <-session.Result:
		if !ok {
			t.Fatal("result channel closed without a value")
		}
		return result
	case <-time.After(bound):
		t.Fatalf("no droid result within %s", bound)
		return Result{}
	}
}

func TestDroidExecuteZeroTimeoutDoesNotSynthesizeDeadline(t *testing.T) {
	t.Parallel()

	// Parent deadline is short so the test finishes quickly. Timeout==0 must
	// not synthesize a 20-minute child deadline: if it did, expiry of this
	// short parent would still be reported as "droid timed out after 20m0s".
	parent, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	session, err := newHangingDroidBackend(t).Execute(parent, "hang", ExecOptions{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	result := awaitDroidResult(t, session, 3*time.Second)

	if strings.Contains(result.Error, "20m") {
		t.Fatalf("Timeout==0 synthesized a 20-minute deadline: status=%q error=%q", result.Status, result.Error)
	}
	if result.Status == "timeout" {
		t.Fatalf("Timeout==0 must not report a configured droid timeout; got status=%q error=%q", result.Status, result.Error)
	}
	if result.Status != "aborted" {
		t.Fatalf("parent deadline with Timeout==0: status=%q error=%q, want aborted", result.Status, result.Error)
	}
}

func TestDroidExecuteZeroTimeoutHasNoChildDeadline(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	session, err := newHangingDroidBackend(t).Execute(parent, "hang", ExecOptions{Timeout: 0})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	go func() {
		for range session.Messages {
		}
	}()

	select {
	case result := <-session.Result:
		t.Fatalf("Timeout==0 imposed a child deadline (got result too early): status=%q error=%q", result.Status, result.Error)
	case <-time.After(250 * time.Millisecond):
	}

	cancel()
	select {
	case result, ok := <-session.Result:
		if !ok {
			t.Fatal("result channel closed without a value")
		}
		if result.Status != "aborted" {
			t.Fatalf("after parent cancel: status=%q error=%q, want aborted", result.Status, result.Error)
		}
		if strings.Contains(result.Error, "timed out") || strings.Contains(result.Error, "20m") {
			t.Fatalf("parent cancel reported as deadline expiry: error=%q", result.Error)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("backend did not return after parent cancellation")
	}
}

func TestDroidExecutePositiveTimeoutTerminatesChild(t *testing.T) {
	t.Parallel()

	const timeout = 100 * time.Millisecond
	session, err := newHangingDroidBackend(t).Execute(context.Background(), "hang", ExecOptions{Timeout: timeout})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	start := time.Now()
	result := awaitDroidResult(t, session, 3*time.Second)
	elapsed := time.Since(start)

	if result.Status != "timeout" {
		t.Fatalf("status=%q error=%q, want timeout", result.Status, result.Error)
	}
	want := "droid timed out after " + timeout.String()
	if result.Error != want {
		t.Fatalf("error=%q, want %q", result.Error, want)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("positive timeout took %s; child was not terminated by the configured deadline", elapsed)
	}
}

func TestDroidExecuteParentCancelWithoutTimeoutIsAborted(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	defer cancel()

	session, err := newHangingDroidBackend(t).Execute(parent, "hang", ExecOptions{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	result := awaitDroidResult(t, session, 3*time.Second)
	elapsed := time.Since(start)

	if result.Status != "aborted" {
		t.Fatalf("status=%q error=%q, want aborted", result.Status, result.Error)
	}
	if result.Error != "execution cancelled" {
		t.Fatalf("error=%q, want %q", result.Error, "execution cancelled")
	}
	if strings.Contains(result.Error, "timed out") {
		t.Fatalf("parent cancellation reported as deadline expiry: error=%q", result.Error)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("parent cancel took %s; child was not aborted promptly", elapsed)
	}
}

func TestDroidToolNameFromTitle(t *testing.T) {
	t.Parallel()
	// Names mirror what `droid exec --output-format stream-json` emits in
	// its system/init event under the `tools` array — see the help output
	// of `droid exec` for the canonical list.
	tests := []struct {
		title string
		want  string
	}{
		{"Read", "read_file"},
		{"Create", "write_file"},
		{"Edit", "edit_file"},
		{"ApplyPatch", "edit_file"},
		{"Execute", "terminal"},
		{"Grep", "search_files"},
		{"Glob", "glob"},
		{"LS", "list_files"},
		{"WebSearch", "web_search"},
		{"FetchUrl", "web_fetch"},
		{"TodoWrite", "todo_write"},
		{"AskUser", "ask_user"},
		{"Task", "task"},
		{"GenerateDroid", "generatedroid"}, // not in mapping → snake_case lowercased
		{"", ""},
	}
	for _, tt := range tests {
		got := droidToolNameFromTitle(tt.title)
		if got != tt.want {
			t.Errorf("droidToolNameFromTitle(%q) = %q, want %q", tt.title, got, tt.want)
		}
	}
}
