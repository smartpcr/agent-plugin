package cordis

import "testing"

func TestCloneConfigCopiesReferencesAndKeepsCycles(t *testing.T) {
	type node struct {
		Next   *node
		Tags   []string
		Extra  map[string]any
		hidden *int
	}
	hidden := 7
	original := &node{Tags: []string{"a"}, Extra: map[string]any{"list": []any{1}}, hidden: &hidden}
	original.Next = original

	clone := cloneConfig(original).(*node)
	if clone == original || clone.Next != clone {
		t.Fatalf("want a distinct copy that keeps its cycle, got %p (original %p, next %p)", clone, original, clone.Next)
	}
	clone.Tags[0] = "b"
	clone.Extra["list"].([]any)[0] = 2
	if original.Tags[0] != "a" || original.Extra["list"].([]any)[0] != 1 {
		t.Fatalf("editing the copy changed the original: %+v", original)
	}
	if clone.hidden != original.hidden {
		t.Fatal("an unexported field was not kept as it is")
	}
	if cloneConfig(nil) != nil {
		t.Fatal("nil config was not kept as nil")
	}
}

func TestCloneConfigKeepsDefinedPointerTypes(t *testing.T) {
	type value struct{ N int }
	type valuePtr *value
	original := valuePtr(&value{N: 1})
	clone, ok := cloneConfig(original).(valuePtr)
	if !ok {
		t.Fatalf("want a valuePtr, got %T", cloneConfig(original))
	}
	if clone == original || (*clone).N != 1 {
		t.Fatalf("want a distinct copy of N=1, got %p (original %p)", clone, original)
	}
}
