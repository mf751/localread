package main

import (
	"path/filepath"
	"testing"
)

func TestSafeJoinRejectsTraversal(t *testing.T) {
	root := t.TempDir()

	bad := []string{
		"",
		"   ",
		"../secret",
		"../../etc/passwd",
		"a/../../etc/passwd",
		"a/b/../../../outside",
		"/etc/passwd",
		"/",
		`\windows\system32`,
		`C:\Windows\System32`,
		"ok/../../../outside",
		"..",
		"nul\x00byte",
		"./../../x",
	}
	for _, rel := range bad {
		if got, err := safeJoin(root, rel); err == nil {
			t.Errorf("safeJoin(%q) = %q, want error", rel, got)
		}
	}
}

func TestSafeJoinAllowsContained(t *testing.T) {
	root := t.TempDir()

	good := map[string]string{
		"a.txt":           filepath.Join(root, "a.txt"),
		"novels/1/x.epub": filepath.Join(root, "novels", "1", "x.epub"),
		"./a/./b":         filepath.Join(root, "a", "b"),
		"a/../b":          filepath.Join(root, "b"),
	}
	for rel, want := range good {
		got, err := safeJoin(root, rel)
		if err != nil {
			t.Errorf("safeJoin(%q) unexpected error: %v", rel, err)
			continue
		}
		if got != want {
			t.Errorf("safeJoin(%q) = %q, want %q", rel, got, want)
		}
	}
}

func TestSafeJoinSiblingPrefixNotConfused(t *testing.T) {
	// root "/x/data" must not accept "/x/data-secret" style escapes.
	root := filepath.Join(string(filepath.Separator), "x", "data")
	if _, err := safeJoin(root, "../data-secret/f"); err == nil {
		t.Fatal("expected traversal to sibling to be rejected")
	}
}

func TestSafeID(t *testing.T) {
	for _, ok := range []string{"abc", "A1-2_3", "0123456789"} {
		if !safeID(ok) {
			t.Errorf("safeID(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{"", "a/b", "a b", "..", "a.b", "a\\b", "x\x00"} {
		if safeID(bad) {
			t.Errorf("safeID(%q) = true, want false", bad)
		}
	}
}
