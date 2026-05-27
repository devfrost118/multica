package agent

import (
	"testing"
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
