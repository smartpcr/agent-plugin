package launch

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// rawFrame is body behind a four-byte big-endian length of n.
func rawFrame(n uint32, body string) []byte {
	out := make([]byte, headerSize, headerSize+len(body))
	binary.BigEndian.PutUint32(out, n)
	return append(out, body...)
}

// frameOf is body as one well-formed frame.
func frameOf(body string) []byte { return rawFrame(uint32(len(body)), body) }

// TestDecodeReadyYieldsBackendAndEffects: a valid ready frame, hand-written or
// encoded, yields its backend, governed effects and canary (TC-04a).
//
//dsh:test DH-T-SANDBOX-4
func TestDecodeReadyYieldsBackendAndEffects(t *testing.T) {
	cases := []struct {
		frame []byte
		want  Ready
	}{
		{frameOf(string(readyBodies[0])), Ready{Backend: sandbox.BackendNone, Governed: []sandbox.Effect{}, Canary: CanaryNotTried}},
		{frameOf(string(readyBodies[1])), Ready{Backend: sandbox.BackendBwrap, Governed: []sandbox.Effect{"fs-write", "network"}, Canary: CanaryRefused}},
		{frameOf(string(readyBodies[2])), Ready{Backend: sandbox.BackendLandlock, Governed: []sandbox.Effect{"fs-write"}, Canary: CanaryWritten}},
	}
	for i, c := range cases {
		got, err := DecodeReady(c.frame)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("case %d: DecodeReady = %+v, %v; want %+v", i, got, err, c.want)
		}
		b, err := EncodeReady(c.want)
		if err != nil {
			t.Fatalf("case %d: EncodeReady: %v", i, err)
		}
		if again, err := DecodeReady(b); err != nil || !reflect.DeepEqual(again, c.want) {
			t.Errorf("case %d: round trip = %+v, %v; want %+v", i, again, err, c.want)
		}
	}
}

// TestDecodeReadyRefusesMalformedFrames: a truncated, empty, oversized or
// malformed frame, or any message but ready, is sandbox/launch-failed and never a
// partial ready (TC-04a).
//
//dsh:test DH-T-SANDBOX-4
func TestDecodeReadyRefusesMalformedFrames(t *testing.T) {
	valid := string(string(readyBodies[0]))
	many := make([]string, maxEffects+1)
	for i := range many {
		many[i] = fmt.Sprintf("%q", fmt.Sprintf("e%d", i))
	}
	cases := map[string][]byte{
		"nothing":            nil,
		"cut header":         {0, 0, 0},
		"cut body":           frameOf(string(readyBodies[1]))[:len(frameOf(string(readyBodies[1])))-1],
		"header only":        rawFrame(uint32(len(valid)), ""),
		"zero length":        rawFrame(0, ""),
		"oversized length":   rawFrame(MaxFrame+1, strings.Repeat(" ", MaxFrame+1)),
		"huge length":        rawFrame(^uint32(0), valid),
		"trailing bytes":     append(frameOf(valid), 0),
		"second frame":       append(frameOf(valid), frameOf(valid)...),
		"second JSON value":  frameOf(string(readyBodies[4])),
		"unknown field":      frameOf(string(readyBodies[3])),
		"not JSON":           frameOf("ready"),
		"JSON array":         frameOf(`["ready"]`),
		"go":                 frameOf(string(readyBodies[5])),
		"exec-failed":        frameOf(string(readyBodies[6])),
		"unknown message":    frameOf(`{"msg":"steady"}`),
		"no backend":         frameOf(`{"msg":"ready","governed":[],"canary":"refused"}`),
		"no governed":        frameOf(`{"msg":"ready","backend":"bwrap","canary":"refused"}`),
		"null governed":      frameOf(`{"msg":"ready","backend":"bwrap","governed":null,"canary":"refused"}`),
		"no canary":          frameOf(`{"msg":"ready","backend":"bwrap","governed":[]}`),
		"error in ready":     frameOf(`{"msg":"ready","backend":"none","governed":[],"canary":"not-tried","error":"x"}`),
		"unknown backend":    frameOf(`{"msg":"ready","backend":"chroot","governed":[],"canary":"refused"}`),
		"empty backend":      frameOf(`{"msg":"ready","backend":"","governed":[],"canary":"refused"}`),
		"unknown canary":     frameOf(`{"msg":"ready","backend":"bwrap","governed":[],"canary":"maybe"}`),
		"upper-case effect":  frameOf(`{"msg":"ready","backend":"bwrap","governed":["FS"],"canary":"refused"}`),
		"empty effect":       frameOf(`{"msg":"ready","backend":"bwrap","governed":[""],"canary":"refused"}`),
		"trailing hyphen":    frameOf(`{"msg":"ready","backend":"bwrap","governed":["fs-"],"canary":"refused"}`),
		"duplicate effect":   frameOf(`{"msg":"ready","backend":"bwrap","governed":["net","net"],"canary":"refused"}`),
		"too many effects":   frameOf(`{"msg":"ready","backend":"bwrap","governed":[` + strings.Join(many, ",") + `],"canary":"refused"}`),
		"wrong type backend": frameOf(`{"msg":"ready","backend":1,"governed":[],"canary":"refused"}`),
	}
	for name, b := range cases {
		got, err := DecodeReady(b)
		if !herr.Has(err, sandbox.CodeLaunchFailed) {
			t.Errorf("%s: DecodeReady = %+v, %v; want %s", name, got, err, sandbox.CodeLaunchFailed)
		}
		if !reflect.DeepEqual(got, Ready{}) {
			t.Errorf("%s: DecodeReady returned a partial ready %+v", name, got)
		}
	}
}

// TestEncodeReadyRefusesInvalidReady: the stage cannot send a ready the host
// would refuse.
func TestEncodeReadyRefusesInvalidReady(t *testing.T) {
	for _, r := range []Ready{
		{},
		{Backend: "chroot", Governed: []sandbox.Effect{}, Canary: CanaryRefused},
		{Backend: sandbox.BackendBwrap, Governed: []sandbox.Effect{}, Canary: "maybe"},
		{Backend: sandbox.BackendBwrap, Governed: []sandbox.Effect{"a b"}, Canary: CanaryRefused},
	} {
		if b, err := EncodeReady(r); !herr.Has(err, sandbox.CodeLaunchFailed) || b != nil {
			t.Errorf("EncodeReady(%+v) = %q, %v; want %s", r, b, err, sandbox.CodeLaunchFailed)
		}
	}
}

// TestReadFrameReportsTheClosedChannel: end of file before a frame's first byte
// is the other end closing the pair; end of file inside a frame is not.
func TestReadFrameReportsTheClosedChannel(t *testing.T) {
	if _, err := readFrame(bytes.NewReader(nil)); !closed(err) || !herr.Has(err, sandbox.CodeLaunchFailed) {
		t.Errorf("readFrame(empty) = %v, want the closed channel as %s", err, sandbox.CodeLaunchFailed)
	}
	for _, b := range [][]byte{{0}, []byte(string(rawSeeds[1]))} {
		if _, err := readFrame(bytes.NewReader(b)); closed(err) || !herr.Has(err, sandbox.CodeLaunchFailed) {
			t.Errorf("readFrame(%q) = %v, want a truncated frame, not a closed channel", b, err)
		}
	}
}

// FuzzDecodeReady: any bytes decode to a valid ready that re-encodes to the
// same ready, or to sandbox/launch-failed; never a panic (DH-T-SANDBOX-4).
//
//dsh:test DH-T-SANDBOX-4
func FuzzDecodeReady(f *testing.F) {
	addReadySeeds(f)
	f.Fuzz(func(t *testing.T, b []byte) {
		r, err := DecodeReady(b)
		if err != nil {
			if !herr.Has(err, sandbox.CodeLaunchFailed) {
				t.Fatalf("DecodeReady(%q) = %v, want %s", b, err, sandbox.CodeLaunchFailed)
			}
			return
		}
		enc, err := EncodeReady(r)
		if err != nil {
			t.Fatalf("EncodeReady(%+v) of a decoded ready: %v", r, err)
		}
		again, err := DecodeReady(enc)
		if err != nil || !reflect.DeepEqual(again, r) {
			t.Fatalf("round trip of %+v = %+v, %v", r, again, err)
		}
	})
}
