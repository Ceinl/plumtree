package components

import (
	"reflect"
	"testing"
)

func TestWrapLines(t *testing.T) {
	cases := []struct {
		name    string
		content string
		w       int
		want    []string
	}{
		{"empty", "", 5, []string{""}},
		{"fits", "hi", 5, []string{"hi"}},
		{"hard wrap", "abcdef", 3, []string{"abc", "def"}},
		{"newline", "ab\ncd", 5, []string{"ab", "cd"}},
		{"newline then wrap", "abcd\nef", 2, []string{"ab", "cd", "ef"}},
		{"zero width", "abc", 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := wrapLines(tc.content, tc.w); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("wrapLines(%q, %d) = %v, want %v", tc.content, tc.w, got, tc.want)
			}
		})
	}
}

func TestTextSetContentDirty(t *testing.T) {
	tx := NewText("a")
	tx.ClearDirty()
	tx.SetContent("a")
	if tx.IsDirty() {
		t.Error("setting same content should not dirty")
	}
	tx.SetContent("b")
	if !tx.IsDirty() {
		t.Error("changing content should dirty")
	}
	if tx.Content() != "b" {
		t.Errorf("Content() = %q, want b", tx.Content())
	}
}
