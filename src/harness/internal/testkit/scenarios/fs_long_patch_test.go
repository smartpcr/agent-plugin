//go:build scenario

package scenarios

import "testing"

// TestFS2PatchOracleReadsTerminatedPatches checks FS-2's own patch reader against
// inputs the product's apply_patch accepts: text after "*** End Patch" (the input's
// trailing newline, a heredoc's closing line) is not chunk content, and a chunk
// ending in "*** End of File" matches only at the end of the file.
func TestFS2PatchOracleReadsTerminatedPatches(t *testing.T) {
	cases := []struct {
		name, before, patch, after string
	}{
		{"terminator with trailing newline", "a\n", "*** Begin Patch\n*** Update File: f\n@@\n-a\n+b\n*** End Patch\n", "b\n"},
		{"terminator without trailing newline", "a\n", "*** Begin Patch\n*** Update File: f\n@@\n-a\n+b\n*** End Patch", "b\n"},
		{"terminator then blank lines", "a\n", "*** Begin Patch\n*** Update File: f\n@@\n-a\n+b\n*** End Patch\n\n\n", "b\n"},
		{"crlf input", "a\n", "*** Begin Patch\r\n*** Update File: f\r\n@@\r\n-a\r\n+b\r\n*** End Patch\r\n", "b\n"},
		{"heredoc wrapper", "a\n", "<<'EOF'\n*** Begin Patch\n*** Update File: f\n@@\n-a\n+b\n*** End Patch\nEOF\n", "b\n"},
		{"padded markers", "a\n", "  *** Begin Patch \n*** Update File: f\n@@\n-a\n+b\n *** End Patch\t\n", "b\n"},
		{"end of file anchors the chunk", "x\ny\nx\n", "*** Begin Patch\n*** Update File: f\n@@\n-x\n+z\n*** End of File\n*** End Patch\n", "x\ny\nz\n"},
		{"blank context line inside a chunk", "a\n\nb\n", "*** Begin Patch\n*** Update File: f\n@@\n a\n\n-b\n+c\n*** End Patch\n", "a\n\nc\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			model := map[string][]byte{"f": []byte(c.before)}
			if n := fs2ApplyPatch(t, model, "clone", c.patch); n != 1 {
				t.Fatalf("patch touched %d files, want 1", n)
			}
			if got := string(model["f"]); got != c.after {
				t.Fatalf("model holds %q, want %q", got, c.after)
			}
		})
	}
	hunks := fs2ParsePatch(t, "*** Begin Patch\n*** Update File: f\n@@\n-a\n+b\n*** End Patch\n")
	if len(hunks) != 1 || len(hunks[0].lines) != 3 {
		t.Fatalf("terminated patch parsed to %+v, want one update of three chunk lines", hunks)
	}
	if _, ok := fs2ApplyChunks("x\ny\n", []string{"@@", "-x", "+z", "*"}); ok {
		t.Fatal("an end-of-file chunk matched a line that is not at the end of the file")
	}
}
