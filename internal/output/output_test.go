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
	root := filepath.FromSlash("/home/me/Nalmeok")
	tests := []struct {
		source string
		kind   string
		want   string
	}{
		{source: "/lectures/강의09.m4a", kind: "file", want: "/home/me/Nalmeok/강의09.md"},
		{source: "/lectures/ch07", kind: "folder", want: "/home/me/Nalmeok/ch07.md"},
		{source: "/lectures/notes", kind: "file", want: "/home/me/Nalmeok/notes.md"},
		{source: "/lectures/ch07/", kind: "folder", want: "/home/me/Nalmeok/ch07.md"},
		{source: "/lectures/algorithm_09.1", kind: "folder", want: "/home/me/Nalmeok/algorithm_09.1.md"},
	}
	for _, tt := range tests {
		source, want := filepath.FromSlash(tt.source), filepath.FromSlash(tt.want)
		if got := TargetPath(root, source, tt.kind); got != want {
			t.Fatalf("TargetPath(%q, %q) = %q, want %q", source, tt.kind, got, want)
		}
	}
}

func TestInside(t *testing.T) {
	root := filepath.FromSlash("/home/me/Nalmeok")
	tests := []struct {
		path string
		want bool
	}{
		{"/home/me/Nalmeok/a.md", true},
		{"/home/me/Nalmeok/알고리즘/1주차.md", true},
		{"/home/me/Nalmeok", false},
		{"/home/me/Nalmeok2/a.md", false},
		{"/home/me/Nalmeok/../a.md", false},
		{"/home/me/a.md", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := Inside(root, filepath.FromSlash(tt.path)); got != tt.want {
			t.Errorf("Inside(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
	if Inside("", filepath.FromSlash("/a.md")) {
		t.Error("an empty folder holds nothing")
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
