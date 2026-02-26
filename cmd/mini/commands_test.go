package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExpandFileRefs(t *testing.T) {
	dir := t.TempDir()

	// Create a test file
	testFile := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(testFile, []byte("world\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrapFile := func(path, content string) string {
		return fmt.Sprintf("\n--- %s ---\n%s\n---\n", path, content)
	}

	tests := []struct {
		name     string
		prompt   string
		want     string
		wantRefs int
		wantErr  bool
	}{
		{
			name:   "no refs",
			prompt: "just a plain prompt",
			want:   "just a plain prompt",
		},
		{
			name:     "existing file includes path label",
			prompt:   "say @" + testFile,
			want:     "say " + wrapFile(testFile, "world"),
			wantRefs: 1,
		},
		{
			name:   "non-existent file left as-is",
			prompt: "hello @/no/such/file.txt there",
			want:   "hello @/no/such/file.txt there",
		},
		{
			name:   "bare @ ignored",
			prompt: "user@ host",
			want:   "user@ host",
		},
		{
			name:     "multiple refs",
			prompt:   "@" + testFile + " and @" + testFile,
			want:     wrapFile(testFile, "world") + " and " + wrapFile(testFile, "world"),
			wantRefs: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, refs, err := expandFileRefs(tt.prompt)
			if (err != nil) != tt.wantErr {
				t.Fatalf("expandFileRefs() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("expandFileRefs() =\n%s\nwant:\n%s", got, tt.want)
			}
			if len(refs) != tt.wantRefs {
				t.Errorf("expandFileRefs() returned %d refs, want %d", len(refs), tt.wantRefs)
			}
		})
	}
}

func TestExpandFileRefs_MultilineFile(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "multi.txt")
	content := "line one\nline two\nline three\n"
	if err := os.WriteFile(testFile, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, refs, err := expandFileRefs("read @" + testFile)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Size != len(content) {
		t.Errorf("expected 1 ref with size %d, got %v", len(content), refs)
	}
	if !strings.Contains(got, "--- "+testFile+" ---") {
		t.Errorf("expected path label, got %q", got)
	}
	if !strings.Contains(got, "line one\nline two\nline three") {
		t.Errorf("expected multiline content, got %q", got)
	}
}
