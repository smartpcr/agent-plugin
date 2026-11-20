//go:build scenario

package scenarios

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand/v2"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
)

// nodefsRounds is the number of power cuts per machine (DH-E-NODEFS-1).
const nodefsRounds = 100

// nodefsHeader starts every version the writer publishes; the payload follows it.
var nodefsHeader = regexp.MustCompile(`^dsh-nodefs v=(\d+) len=(\d+) sha=([0-9a-f]{64})\n`)

// nodefsVersion is version n of the replaced file: a header naming the payload's
// length and SHA-256, then 64 KiB of payload, so a torn file is detected.
func nodefsVersion(n int) []byte {
	payload := strings.Repeat(fmt.Sprintf("%08d", n), 8192)
	sum := sha256.Sum256([]byte(payload))
	return []byte(fmt.Sprintf("dsh-nodefs v=%d len=%d sha=%s\n%s", n, len(payload), hex.EncodeToString(sum[:]), payload))
}

// TestDurableReplaceSurvivesPowerLoss: a durable replace is complete and never
// older than its acknowledgement after a power cut (libs/node-fs-path-os-zlib.md
// DH-E-NODEFS-1; DH-LIB-NODEFS-R1; DH-D-FS-1). Executed in DH-S-18 (step 18.11).
//
// Given a virtual machine for each of Linux, macOS and Windows with a disk that can
// be cut off (a power-off, not a clean shutdown), running a loop that replaces a
// file durably with fsx.WriteFile and records each acknowledged version on this
// second machine, when the machine is powered off at a random moment and restarted,
// 100 times, then the file is always complete and never older than the last
// acknowledged version. Cleanup: deleting the virtual disks is the operator's; the
// version log of a failing run is written to DSH_SCENARIO_REPORT.
//
// The test has two roles. Inside a machine (DSH_SCENARIO_NODEFS_ROLE=writer) it is
// the loop: it replaces DSH_SCENARIO_NODEFS_FILE durably and sends each published
// version to DSH_SCENARIO_NODEFS_ACK until the machine loses power. On the second
// machine it is the controller, with DSH_SCENARIO_VMS (";"-separated machine names),
// DSH_SCENARIO_NODEFS_LISTEN (the address the acknowledgements arrive at) and shell
// command lines over {vm}: DSH_SCENARIO_NODEFS_START starts the writer role in the
// machine in the background, DSH_SCENARIO_NODEFS_POWEROFF cuts its power,
// DSH_SCENARIO_NODEFS_POWERON starts it and returns once it is reachable, and
// DSH_SCENARIO_NODEFS_READ prints the file.
//
//dsh:scenario DH-E-NODEFS-1
func TestDurableReplaceSurvivesPowerLoss(t *testing.T) {
	if os.Getenv("DSH_SCENARIO_NODEFS_ROLE") == "writer" {
		nodefsWriter(t)
		return
	}
	vms := needList(t, "DSH_SCENARIO_VMS", "the virtual machines, ';'-separated")
	listen := need(t, "DSH_SCENARIO_NODEFS_LISTEN", "address the writers' acknowledgements arrive at")
	start := need(t, "DSH_SCENARIO_NODEFS_START", "command starting the writer role in {vm} in the background")
	powerOff := need(t, "DSH_SCENARIO_NODEFS_POWEROFF", "command cutting the power of {vm}")
	powerOn := need(t, "DSH_SCENARIO_NODEFS_POWERON", "command starting {vm} and waiting until it is reachable")
	read := need(t, "DSH_SCENARIO_NODEFS_READ", "command printing the replaced file of {vm}")
	reportPath := need(t, "DSH_SCENARIO_REPORT", "file the version log is written to")

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	acked := map[string]int{} // vm -> last acknowledged version
	var log []string
	var conns []net.Conn
	var wg sync.WaitGroup
	// A writer's connection dies with its machine and never closes: close them all.
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, conn := range conns {
			_ = conn.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer conn.Close()
				sc := bufio.NewScanner(conn)
				for sc.Scan() {
					vm, v, ok := strings.Cut(sc.Text(), " ")
					n, err := strconv.Atoi(v)
					if !ok || err != nil {
						continue
					}
					mu.Lock()
					acked[vm] = max(acked[vm], n)
					log = append(log, fmt.Sprintf("%s ack %s %d", time.Now().Format(time.RFC3339Nano), vm, n))
					mu.Unlock()
				}
			}()
		}
	}()
	t.Cleanup(func() {
		if t.Failed() {
			mu.Lock()
			defer mu.Unlock()
			_ = os.WriteFile(reportPath, []byte(strings.Join(log, "\n")+"\n"), 0o600)
		}
	})

	for _, vm := range vms {
		vars := map[string]string{"vm": vm}
		for round := 1; round <= nodefsRounds; round++ {
			mustShell(t, expand(start, vars))
			<-time.After(time.Second + rand.N(4*time.Second))
			mustShell(t, expand(powerOff, vars))
			mu.Lock()
			last := acked[vm]
			log = append(log, fmt.Sprintf("%s cut %s round %d after ack %d", time.Now().Format(time.RFC3339Nano), vm, round, last))
			mu.Unlock()
			if last == 0 {
				t.Fatalf("%s round %d: no version was acknowledged before the cut", vm, round)
			}
			mustShell(t, expand(powerOn, vars))
			data := mustShell(t, expand(read, vars))
			m := nodefsHeader.FindStringSubmatch(data)
			if m == nil {
				t.Fatalf("%s round %d: the file is not a complete version: %.80q", vm, round, data)
			}
			n, _ := strconv.Atoi(m[1])
			size, _ := strconv.Atoi(m[2])
			payload := data[len(m[0]):]
			sum := sha256.Sum256([]byte(payload[:min(size, len(payload))]))
			if len(payload) < size || hex.EncodeToString(sum[:]) != m[3] {
				t.Fatalf("%s round %d: version %d is torn", vm, round, n)
			}
			if n < last {
				t.Fatalf("%s round %d: the file holds version %d, older than the acknowledged %d", vm, round, n, last)
			}
			mu.Lock()
			log = append(log, fmt.Sprintf("%s read %s round %d version %d", time.Now().Format(time.RFC3339Nano), vm, round, n))
			mu.Unlock()
		}
	}
}

// nodefsWriter is the writer role: replace the file durably, acknowledge each
// published version, until the machine loses power. It continues from the version
// on disk, so versions only grow across rounds.
func nodefsWriter(t *testing.T) {
	path := need(t, "DSH_SCENARIO_NODEFS_FILE", "the file the writer replaces")
	ack := need(t, "DSH_SCENARIO_NODEFS_ACK", "address of the controller's acknowledgement listener")
	vm := need(t, "DSH_SCENARIO_NODEFS_VM", "the name of this machine, as the controller knows it")
	conn, err := net.Dial("tcp", ack)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	n := 0
	if data, err := os.ReadFile(path); err == nil {
		if m := nodefsHeader.FindSubmatch(data); m != nil {
			n, _ = strconv.Atoi(string(m[1]))
		}
	}
	for t.Context().Err() == nil {
		n++
		if err := fsx.WriteFile(path, nodefsVersion(n), fsx.Options{Perm: 0o600, Durable: true}); err != nil {
			t.Fatalf("version %d: %v", n, err)
		}
		if _, err := fmt.Fprintf(conn, "%s %d\n", vm, n); err != nil {
			t.Fatalf("acknowledging %d: %v", n, err)
		}
	}
}
