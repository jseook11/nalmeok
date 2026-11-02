package output

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestMerge(t *testing.T) {
	tests := []struct {
		name  string
		texts []string
		want  string
	}{
		{name: "empty", texts: nil, want: ""},
		{name: "only blanks", texts: []string{"  ", "\n", ""}, want: ""},
		{name: "trims and joins", texts: []string{"  첫째  ", "", "둘째\n", "\n셋째\n"}, want: "첫째\n\n둘째\n\n셋째\n"},
		{name: "single", texts: []string{" 하나 "}, want: "하나\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Merge(tt.texts); got != tt.want {
				t.Fatalf("Merge(%q) = %q, want %q", tt.texts, got, tt.want)
			}
		})
	}
}

func TestTargetPath(t *testing.T) {
	tests := []struct {
		source string
		kind   string
		want   string
	}{
		{source: "/lectures/강의09.m4a", kind: "file", want: "/lectures/강의09.md"},
		{source: "/lectures/ch07", kind: "folder", want: "/lectures/ch07/ch07.md"},
		{source: "/lectures/notes", kind: "file", want: "/lectures/notes.md"},
		{source: "/lectures/ch07/", kind: "folder", want: "/lectures/ch07/ch07.md"},
	}
	for _, tt := range tests {
		if got := TargetPath(tt.source, tt.kind); got != tt.want {
			t.Fatalf("TargetPath(%q, %q) = %q, want %q", tt.source, tt.kind, got, tt.want)
		}
	}
}

func TestUniquePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "강의09.md")
	if got := UniquePath(path); got != path {
		t.Fatalf("UniquePath on missing file = %q, want %q", got, path)
	}

	if err := os.WriteFile(path, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	two := filepath.Join(dir, "강의09 (2).md")
	if got := UniquePath(path); got != two {
		t.Fatalf("first collision = %q, want %q", got, two)
	}
	if err := os.WriteFile(two, []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}
	three := filepath.Join(dir, "강의09 (3).md")
	if got := UniquePath(path); got != three {
		t.Fatalf("second collision = %q, want %q", got, three)
	}
}

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "결과.md")
	if err := Write(path, "첫 번째\n"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "첫 번째\n" {
		t.Fatalf("content = %q", data)
	}

	if err := Write(path, "두 번째\n"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "두 번째\n" {
		t.Fatalf("overwritten content = %q", data)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o644 {
			t.Fatalf("perm = %o, want 644", perm)
		}
	}
}

func TestWriteLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.md")
	if err := Write(path, "text"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "a.md" {
		t.Fatalf("directory entries = %v, want just a.md", entries)
	}
}
