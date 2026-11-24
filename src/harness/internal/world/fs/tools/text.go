package tools

import (
	"bytes"
	"context"
	"io"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/ops"
)

// utf8BOM is the UTF-8 byte-order mark; a file that starts with it keeps it.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// checkText is the one text check of read and edit, and the whole of the
// unconfirmed default "text tools are UTF-8 only" (impl.md sec. 9.1, core
// sec. 6.3): content is text when, after an optional UTF-8 byte-order mark, it
// is valid UTF-8 and holds no NUL byte. Everything else, UTF-16 behind a mark
// included, is refused with fs/not-text and is never decoded by guess. It
// returns the text without the mark and whether the mark was there.
//
// Should the owner confirm DH-ADR-FS-11 (a) instead (UTF-16 behind a mark is
// text), this function is the one place to change: decode with fsx.Decode and
// re-encode with fsx.Encode on write.
func checkText(display string, b []byte) (text string, bom bool, err error) {
	if bom = bytes.HasPrefix(b, utf8BOM); bom {
		b = b[len(utf8BOM):]
	}
	if bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b) {
		return "", false, herr.Newf(fs.CodeNotText, "%s is not UTF-8 text; the text tools read and edit UTF-8 only", display)
	}
	return string(b), bom, nil
}

// readAll opens r through o and reads at most max bytes of it; a longer file is
// fs/too-large. The info carries the version read.
func readAll(ctx context.Context, o *ops.Service, c ops.Call, r fs.Ref, max int) ([]byte, fs.Info, error) {
	rc, in, err := o.Open(ctx, c, r)
	if err != nil {
		return nil, fs.Info{}, err
	}
	defer rc.Close()
	if in.Size > int64(max) {
		return nil, fs.Info{}, herr.Newf(fs.CodeTooLarge, "%s is %d bytes, more than the %d a text tool handles", in.Target.Display, in.Size, max)
	}
	b, err := io.ReadAll(io.LimitReader(rc, int64(max)+1))
	if err != nil {
		return nil, fs.Info{}, herr.Wrap(fs.CodeIO, err, "read "+in.Target.Display)
	}
	if len(b) > max {
		return nil, fs.Info{}, herr.Newf(fs.CodeTooLarge, "%s is more than the %d bytes a text tool handles", in.Target.Display, max)
	}
	return b, in, nil
}
