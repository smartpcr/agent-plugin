// Package errdiscard seeds the errdiscard analyzer.
package errdiscard

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

func f() error            { return errors.New("x") }
func g() (int, error)     { return 0, nil }
func h() int              { return 0 }
func p() *os.PathError    { return nil }
func two() (error, error) { return nil, nil }

func cases() {
	_ = f()      // want `error result of f assigned to _`
	n, _ := g()  // want `error result of g assigned to _`
	_, _ = two() // want `error result of two assigned to _` `error result of two assigned to _`
	_ = p()      // want `error result of p assigned to _`
	var _ = f()  // want `error result of f assigned to _`
	f()          // want `error result of f dropped by a call statement`
	(f())        // want `error result of f dropped by a call statement`
	g()          // want `error result of g dropped by a call statement`
	defer f()    // want `error result of f dropped by defer`
	go f()       // want `error result of f dropped by go`

	// Not reported: no error result, not a call, or an exempt call.
	h()
	_ = n
	_, err := g()
	_ = err
	_ = f
	fmt.Println("ok")
	fmt.Printf("%d\n", 1)
	fmt.Print()
	fmt.Fprintln(os.Stderr, "x")
	fmt.Fprintf(os.Stderr, "%d", 1)
	var b bytes.Buffer
	b.WriteString("x")
	b.Write(nil)
	var sb strings.Builder
	sb.WriteString("x")
	sb.WriteByte('x')
	sb.WriteRune('x')

	// A reasoned directive accepts the site; a reasonless one accepts nothing.
	_ = f() //lint:ignore errdiscard the fixture accepts this one, with a reason
	//lint:ignore errdiscard the directive above a statement applies to it
	f()
	//lint:ignore errdiscard
	_ = f() // want `error result of f assigned to _`
	//lint:ignore typeassert a reason for another check suppresses nothing here
	_ = f() // want `error result of f assigned to _`
}

// closes: a deferred Close of a read-only handle is exempt; of anything written, or
// not deferred, it is reported.
func closes(path string, param *os.File, rc io.ReadCloser, wc io.WriteCloser, resp *http.Response) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r, err := os.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer r.Close()
	root, err := os.OpenRoot(path)
	if err != nil {
		return err
	}
	defer root.Close() // want `error result of root.Close dropped by defer`
	rf, err := root.Open("x")
	if err != nil {
		return err
	}
	defer rf.Close()
	defer rc.Close()
	defer resp.Body.Close()

	wf, err := os.Create(path)
	if err != nil {
		return err
	}
	defer wf.Close()    // want `error result of wf.Close dropped by defer`
	defer param.Close() // want `error result of param.Close dropped by defer`
	defer wc.Close()    // want `error result of wc.Close dropped by defer`
	rw, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer rw.Close() // want `error result of rw.Close dropped by defer`
	f.Close()        // want `error result of f.Close dropped by a call statement`
	return nil
}
