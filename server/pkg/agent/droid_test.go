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
	tests := []struct {
		title string
		want  string
	}{
		{"Read file: /tmp/foo.go", "read_file"},
		{"Write: /tmp/bar.go", "write_file"},
		{"Patch: /tmp/x", "edit_file"},
		{"Shell: ls -la", "terminal"},
		{"Run command: pwd", "terminal"},
		{"grep", "search_files"},
		{"Glob: *.go", "glob"},
		{"Code", "code"},
		{"Todo List", "todo_write"},
		{"Custom Thing", "custom_thing"},
		{"", ""},
	}
	for _, tt := range tests {
		got := droidToolNameFromTitle(tt.title)
		if got != tt.want {
			t.Errorf("droidToolNameFromTitle(%q) = %q, want %q", tt.title, got, tt.want)
		}
	}
}
