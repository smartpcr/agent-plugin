package jsonrpc

import (
	"context"
	"io"
)

// ServeOne serves a one-shot connection (the tool endpoint of DH-D-TOOLS-4): it reads
// one request within o.MaxFrameBytes, dispatches it through o.Methods, writes its
// one reply, waits with Barrier until that reply is written, then closes the
// connection and, when r is an io.Closer, closes r. The end of the peer's input after
// the request does not cancel the handler, so a peer may half-close once it has
// sent the request.
//
// It returns nil once the reply is written. A first frame that is not a request (a
// notification, a response, a line that does not decode or is over the bound) is
// answered with its table error and returned; the input ending before any frame
// returns jsonrpc/closed. A second frame that arrives before the reply is written is
// answered jsonrpc/invalid-request, the request's reply is still written, and that
// refusal is returned. Ending ctx ends the connection and cancels the handler.
func ServeOne(ctx context.Context, r io.Reader, w io.Writer, o Options) error {
	c := start(ctx, r, w, o, true)
	defer closeReader(r)
	select {
	case <-c.replied:
	case <-c.done:
		return c.Err()
	}
	if err := c.Barrier(ctx); err != nil {
		if cause := c.Err(); cause != nil {
			err = cause
		}
		c.Close()
		return err
	}
	c.Close()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.refusal
}

// closeReader closes r when it can be closed, which ends ServeOne's blocked read.
func closeReader(r io.Reader) {
	if rc, ok := r.(io.Closer); ok {
		_ = rc.Close()
	}
}
