package sqlite

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"time"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// The export file (session-store.md §6.2, DH-ADR-STORE-9, DH-D-STORE-7). Line 1
// is the header, {"dsh_export":1,"session":{...},"inherited":n}; each following
// line is one event with its envelope fields and its data and aux as the stored
// strings; the last line is the trailer, {"dsh_export_end":{"events":n,"sha256":h}},
// where h is the SHA-256 of every byte before it, so a truncated or altered file is
// refused. Every line ends in "\n". The file is deterministic: the same session
// exports to the same bytes. It may be gzip compressed; Import detects that.

// ExportFormat is the format number of the export file.
const ExportFormat = 1

// DefaultImportLimit bounds the decompressed size of a file Import reads.
const DefaultImportLimit int64 = 1 << 30

// exportPage is the number of events Export reads per query.
const exportPage = 512

const (
	// CodeInvalidExport refuses a file that is not a whole, unaltered export of
	// this format: nothing is written.
	CodeInvalidExport store.Code = "store/invalid-export"
	// CodeExportTooLarge refuses a file whose decompressed size passes the limit.
	CodeExportTooLarge store.Code = "store/export-too-large"
)

// ExportOptions are Export's options.
type ExportOptions struct {
	// Gzip compresses the file.
	Gzip bool
}

// ImportOptions are Import's options.
type ImportOptions struct {
	// NewID, when set, imports the session under this id instead of the file's.
	NewID string
	// Limit bounds the decompressed size of the file; 0 is DefaultImportLimit.
	Limit int64
}

type exportHeader struct {
	Format    int           `json:"dsh_export"`
	Session   *store.Header `json:"session"`
	Inherited int           `json:"inherited"`
}

type exportEvent struct {
	Seq       store.Seq    `json:"seq"`
	Time      int64        `json:"time"`
	Type      string       `json:"type"`
	PV        int          `json:"pv"`
	Ignorable bool         `json:"ignorable,omitempty"`
	Op        event.OpKind `json:"op,omitempty"`
	Start     store.Seq    `json:"start,omitempty"`
	End       store.Seq    `json:"end,omitempty"`
	Sources   []store.Seq  `json:"sources,omitempty"`
	Data      string       `json:"data"`
	Aux       string       `json:"aux,omitempty"`
}

type exportEnd struct {
	Events int64  `json:"events"`
	SHA256 string `json:"sha256"`
}

type exportTrailer struct {
	End exportEnd `json:"dsh_export_end"`
}

// exportLine is any line after the header: an event, or the trailer when End is
// set.
type exportLine struct {
	exportEvent
	End *exportEnd `json:"dsh_export_end"`
}

// Export writes session id to w in the export format, from one read snapshot.
// It copies the stored strings and never re-encodes a payload.
func (s *Store) Export(ctx context.Context, w io.Writer, id string, o ExportOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var gz *gzip.Writer
	out := w
	if o.Gzip {
		gz = gzip.NewWriter(w) // no name and no time: the bytes are deterministic
		out = gz
	}
	bw := bufio.NewWriter(out)
	sum := sha256.New()
	err := s.read(ctx, func(tx *sql.Tx) error {
		r, err := liveByID(ctx, tx, id)
		if err != nil {
			return err
		}
		h := r.sum.Header
		h.CreatedAt = h.CreatedAt.UTC()
		if err := writeLine(bw, sum, exportHeader{Format: ExportFormat, Session: &h, Inherited: h.InheritedCount}); err != nil {
			return err
		}
		var n int64
		for from := store.Seq(0); ; {
			recs, err := selectRecords(ctx, tx, r.uid, from, exportPage, true)
			if err != nil {
				return err
			}
			for _, rec := range recs {
				if rec.Seq != store.Seq(n) {
					return store.Errorf(CodeCorrupt, "session %q: seq %d where %d was expected", id, rec.Seq, n)
				}
				if !utf8.ValidString(rec.Payload) || !utf8.ValidString(rec.Cold) {
					return store.Errorf(CodeCorrupt, "session %q: event %d is not UTF-8 text", id, rec.Seq)
				}
				if err := writeLine(bw, sum, eventLine(rec)); err != nil {
					return err
				}
				n++
			}
			if len(recs) < exportPage {
				break
			}
			from = recs[len(recs)-1].Seq + 1
		}
		if n != int64(r.sum.NextSeq) {
			return store.Errorf(CodeCorrupt, "session %q: %d events below next seq %d", id, n, r.sum.NextSeq)
		}
		return writeLine(bw, nil, exportTrailer{End: exportEnd{Events: n, SHA256: hex.EncodeToString(sum.Sum(nil))}})
	})
	if err != nil {
		return err
	}
	if err := bw.Flush(); err != nil {
		return ioErr("export", err)
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			return ioErr("export", err)
		}
	}
	return nil
}

func eventLine(r store.Record) exportEvent {
	return exportEvent{Seq: r.Seq, Time: r.Time, Type: r.Type, PV: r.Version, Ignorable: r.Ignorable, Op: r.Op,
		Start: r.Start, End: r.End, Sources: r.Sources, Data: r.Payload, Aux: r.Cold}
}

func (e exportEvent) record() store.Record {
	return store.Record{Seq: e.Seq, Time: e.Time, Type: e.Type, Version: e.PV, Ignorable: e.Ignorable, Op: e.Op,
		Start: e.Start, End: e.End, Sources: e.Sources, Payload: e.Data, Cold: e.Aux}
}

// writeLine writes v as one JSON line, and into sum when it is set.
func writeLine(w io.Writer, sum hash.Hash, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil { // Encode ends the line with "\n"
		return err
	}
	if sum != nil {
		sum.Write(buf.Bytes())
	}
	if _, err := w.Write(buf.Bytes()); err != nil {
		return ioErr("export", err)
	}
	return nil
}

func ioErr(op string, err error) error {
	return &store.Error{Code: CodeIO, Msg: op + ": " + err.Error(), Err: err}
}

func invalidExport(format string, args ...any) error {
	return store.Errorf(CodeInvalidExport, "import: "+format, args...)
}

// Import reads one export file from r and stores its session, unheld. The whole
// file is read and validated first (format, trailer count and digest, contiguous
// seqs from 0, every record admitted by the registry), then written in one
// transaction, so a refused file writes nothing. A gzip file is decompressed, at
// most o.Limit bytes of it. An id the home already holds is refused with
// session/exists unless o.NewID names another. It returns the imported header.
func (s *Store) Import(ctx context.Context, r io.Reader, o ImportOptions) (store.Header, error) {
	if err := ctx.Err(); err != nil {
		return store.Header{}, err
	}
	limit := o.Limit
	if limit <= 0 {
		limit = DefaultImportLimit
	}
	h, recs, err := s.parseExport(r, limit)
	if err != nil {
		return store.Header{}, err
	}
	from := h.ID
	if o.NewID != "" {
		h.ID = o.NewID
	}
	var last int64
	if n := len(recs); n > 0 && recs[n-1].Time > 0 {
		last = s.eventNanos(recs[n-1].Time)
	}
	err = s.write(ctx, func(tx *sql.Tx) error {
		if err := absent(ctx, tx, h.ID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO sessions (id, created_at, cwd, origin, creator, fork_source,
			fork_through, inherited_count, delegation_depth, preset, next_seq, last_event_at, imported_from)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			h.ID, h.CreatedAt.UnixNano(), h.Cwd, h.Origin, h.Creator, h.ForkSource, int64(h.ForkThrough),
			h.InheritedCount, h.DelegationDepth, h.Preset, len(recs), last, from)
		if err != nil {
			return err
		}
		uid, err := res.LastInsertId()
		if err != nil {
			return err
		}
		size, err := insertRecords(ctx, tx, uid, recs)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE sessions SET bytes = ? WHERE uid = ?`, size, uid)
		return err
	})
	if err != nil {
		return store.Header{}, err
	}
	return h, nil
}

// parseExport reads and validates a whole export file.
func (s *Store) parseExport(r io.Reader, limit int64) (store.Header, []store.Record, error) {
	br := bufio.NewReader(r)
	var in io.Reader = br
	if magic, _ := br.Peek(2); len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b {
		gz, err := gzip.NewReader(br)
		if err != nil {
			return store.Header{}, nil, invalidExport("gzip: %v", err)
		}
		defer gz.Close()
		in = gz
	}
	lines := bufio.NewReader(&limitReader{r: in, max: limit})
	sum := sha256.New()
	next := func() ([]byte, error) {
		line, err := lines.ReadBytes('\n')
		var tooLarge *tooLargeError
		switch {
		case errors.As(err, &tooLarge):
			return nil, store.Errorf(CodeExportTooLarge, "import: the file passes %d bytes", limit)
		case errors.Is(err, io.EOF):
			if len(line) == 0 {
				return nil, invalidExport("the file ends before its trailer")
			}
			return nil, invalidExport("the file ends inside a line")
		case err != nil:
			return nil, invalidExport("read: %v", err)
		}
		return line, nil
	}
	first, err := next()
	if err != nil {
		return store.Header{}, nil, err
	}
	sum.Write(first)
	var hdr exportHeader
	if err := json.Unmarshal(first, &hdr); err != nil {
		return store.Header{}, nil, invalidExport("header: %v", err)
	}
	if hdr.Format != ExportFormat {
		return store.Header{}, nil, invalidExport("format %d, this build reads %d", hdr.Format, ExportFormat)
	}
	if hdr.Session == nil || hdr.Session.ID == "" {
		return store.Header{}, nil, invalidExport("the header names no session")
	}
	if hdr.Inherited != hdr.Session.InheritedCount {
		return store.Header{}, nil, invalidExport("header: inherited %d, session says %d", hdr.Inherited, hdr.Session.InheritedCount)
	}
	h := *hdr.Session
	var recs []store.Record
	for {
		line, err := next()
		if err != nil {
			return store.Header{}, nil, err
		}
		var l exportLine
		if err := json.Unmarshal(line, &l); err != nil {
			return store.Header{}, nil, invalidExport("line %d: %v", len(recs)+2, err)
		}
		if l.End != nil {
			if got := hex.EncodeToString(sum.Sum(nil)); l.End.SHA256 != got {
				return store.Header{}, nil, invalidExport("digest %s, the file hashes to %s", l.End.SHA256, got)
			}
			if l.End.Events != int64(len(recs)) {
				return store.Header{}, nil, invalidExport("trailer counts %d events, the file holds %d", l.End.Events, len(recs))
			}
			if _, err := lines.ReadByte(); !errors.Is(err, io.EOF) {
				return store.Header{}, nil, invalidExport("bytes after the trailer")
			}
			break
		}
		sum.Write(line)
		rec := l.record()
		if rec.Seq != store.Seq(len(recs)) {
			return store.Header{}, nil, invalidExport("seq %d where %d was expected", rec.Seq, len(recs))
		}
		recs = append(recs, rec)
	}
	if h.InheritedCount > len(recs) {
		return store.Header{}, nil, invalidExport("%d inherited events, the file holds %d", h.InheritedCount, len(recs))
	}
	if err := store.CheckOpen(s.reg, recs); err != nil {
		return store.Header{}, nil, fmt.Errorf("import: %w", err)
	}
	if h.CreatedAt.IsZero() {
		h.CreatedAt = time.Unix(0, 0)
	}
	return h, recs, nil
}

type tooLargeError struct{}

func (*tooLargeError) Error() string { return "the input passes its size limit" }

// limitReader reads at most max bytes of r and fails with tooLargeError when r
// holds more, so a small file that expands hugely is stopped at the limit.
type limitReader struct {
	r      io.Reader
	n, max int64
}

func (l *limitReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if l.n >= l.max {
		var one [1]byte
		for {
			k, err := l.r.Read(one[:])
			if k > 0 {
				return 0, &tooLargeError{}
			}
			if err != nil {
				return 0, err
			}
		}
	}
	if rest := l.max - l.n; int64(len(p)) > rest {
		p = p[:rest]
	}
	k, err := l.r.Read(p)
	l.n += int64(k)
	return k, err
}
