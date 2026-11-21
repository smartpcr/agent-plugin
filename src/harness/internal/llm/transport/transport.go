package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
)

// Kind is the egress data kind of a model request.
const Kind = "model-request"

// DefaultIdle is how long a response body may send nothing before the attempt
// fails with llm/timeout.
const DefaultIdle = 5 * time.Minute

// MaxErrorBody bounds the error body read for classification.
const MaxErrorBody = 64 << 10

// MaxRetryAfter is the largest retry-after a failure records; a longer one is
// dropped as invalid. Capping the wait is the retry policy's decision (DH-D-LLM-3).
const MaxRetryAfter = 24 * time.Hour

// Options configure a transport.
type Options struct {
	// Label names the egress client.
	Label string
	// HeaderWait bounds the wait for response headers; zero waits for the context.
	HeaderWait time.Duration
	// Idle bounds the silence between body reads; zero is DefaultIdle.
	Idle time.Duration
	// Owner is the asker's lifetime; when it ends the egress client is released.
	Owner context.Context
	// Now is the clock an HTTP-date retry-after is read against; nil is time.Now.
	Now func() time.Time
}

// Transport sends model requests. It is safe for concurrent use.
type Transport struct {
	client  *http.Client
	release func()
	idle    time.Duration
	now     func() time.Time
}

// New is a transport whose client egress hands out under p for Kind. Without a
// permit for Kind it fails with egress/not-permitted; a permit that does not name
// a request's destination fails that request, before any dial.
func New(svc *egress.Service, p egress.Permit, o Options) (*Transport, error) {
	if svc == nil {
		return nil, errors.New("transport: no egress service")
	}
	c, err := svc.Client(Kind, p, egress.Options{Label: o.Label, HeaderWait: o.HeaderWait, Owner: o.Owner})
	if err != nil {
		return nil, err
	}
	t := over(c, o)
	t.release = func() { svc.Release(c) }
	return t, nil
}

// Over is a transport over rt with redirects refused: the seam for the test
// kit's fake transport and recorded fixtures. Production transports come from New.
func Over(rt http.RoundTripper, o Options) *Transport {
	return over(&http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}, o)
}

func over(c *http.Client, o Options) *Transport {
	t := &Transport{client: c, release: func() {}, idle: o.Idle, now: o.Now}
	if t.idle <= 0 {
		t.idle = DefaultIdle
	}
	if t.now == nil {
		t.now = time.Now
	}
	return t
}

// Close releases the egress client; later requests fail with egress/not-permitted.
func (t *Transport) Close() { t.release() }

// Classifier turns an unsuccessful response into a failure: status, headers and
// at most MaxErrorBody bytes of body. A nil result falls back to StatusFailure.
// The transport fills Status, RetryAfter and RequestID when the classifier left
// them unset.
type Classifier func(status int, h http.Header, body []byte) *vocab.Failure

// Request is one model request a driver built.
type Request struct {
	URL    string
	Header http.Header
	Body   []byte
	// Classify classifies a non-2xx response; nil is StatusFailure.
	Classify Classifier
}

// Response is a 2xx response. Body is idle-timed: a read after Idle with no
// bytes fails with a *vocab.Failure of llm/timeout, and any other read error with
// one of llm/transport. The caller closes Body.
type Response struct {
	Status    int
	Header    http.Header
	Body      io.ReadCloser
	RequestID string
}

// Post sends r. A non-2xx response is read up to MaxErrorBody, closed and
// returned as its *vocab.Failure; a request egress refuses is llm/invalid-request
// naming egress/not-permitted (permanent, never retried); a dial or header-wait
// failure is llm/transport or llm/timeout. A cancelled ctx returns ctx's error.
func (t *Transport) Post(ctx context.Context, r Request) (*Response, error) {
	actx, cancel := context.WithCancelCause(ctx)
	hreq, err := http.NewRequestWithContext(actx, http.MethodPost, r.URL, bytes.NewReader(r.Body))
	if err != nil {
		cancel(nil)
		return nil, fail(vocab.CodeInvalidRequest, "the request cannot be built: %v", err)
	}
	hreq.ContentLength = int64(len(r.Body))
	for k, vs := range r.Header {
		for _, v := range vs {
			hreq.Header.Add(k, v)
		}
	}
	resp, err := t.client.Do(hreq)
	if err != nil {
		cancel(nil)
		if ctx.Err() != nil {
			return nil, context.Cause(ctx)
		}
		return nil, doFailure(err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, MaxErrorBody))
		_ = resp.Body.Close()
		cancel(nil)
		return nil, t.classify(r.Classify, resp.StatusCode, resp.Header, body)
	}
	body := &idleBody{rc: resp.Body, cancel: cancel, parent: ctx, ctx: actx}
	body.timer = time.AfterFunc(t.idle, func() { cancel(errIdle) })
	body.idle = t.idle
	return &Response{Status: resp.StatusCode, Header: resp.Header, Body: body, RequestID: RequestID(resp.Header)}, nil
}

func (t *Transport) classify(c Classifier, status int, h http.Header, body []byte) *vocab.Failure {
	var f *vocab.Failure
	if c != nil {
		f = c(status, h, body)
	}
	if f == nil {
		f = StatusFailure(status, h, body)
	}
	if f.Status == 0 {
		f.Status = status
	}
	if f.RetryAfter == 0 {
		if d, ok := RetryAfter(h, t.now()); ok {
			f.RetryAfter = d
		}
	}
	if f.RequestID == "" {
		f.RequestID = RequestID(h)
	}
	return f
}

// StatusFailure classifies by status alone: 401 and 403 are llm/auth, 408
// llm/timeout, 429 llm/rate-limit, 5xx (and 529) llm/server, any other status
// llm/http-<status>. The message names the status only, never the body.
func StatusFailure(status int, _ http.Header, _ []byte) *vocab.Failure {
	var code vocab.Code
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		code = vocab.CodeAuth
	case status == http.StatusRequestTimeout:
		code = vocab.CodeTimeout
	case status == http.StatusTooManyRequests:
		code = vocab.CodeRateLimit
	case status >= 500 && status <= 599:
		code = vocab.CodeServer
	case status >= 400 && status <= 499:
		code = vocab.HTTPCode(status)
	default:
		return withStatus(fail(vocab.CodeMalformedResponse, "unexpected status %d", status), status)
	}
	return withStatus(fail(code, "status %d %s", status, http.StatusText(status)), status)
}

func withStatus(f *vocab.Failure, status int) *vocab.Failure {
	if status >= 100 && status <= 599 {
		f.Status = status
	}
	return f
}

// Fail is a failure of code; code must be in the closed table.
func Fail(code vocab.Code, format string, args ...any) *vocab.Failure {
	return fail(code, format, args...)
}

func fail(code vocab.Code, format string, args ...any) *vocab.Failure {
	f, err := vocab.NewFailure(code, fmt.Sprintf(format, args...))
	if err != nil {
		panic(err)
	}
	return f
}

func doFailure(err error) *vocab.Failure {
	if egress.IsNotPermitted(err) {
		return fail(vocab.CodeInvalidRequest, "%v", err)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fail(vocab.CodeTimeout, "%v", err)
	}
	return fail(vocab.CodeTransport, "%v", err)
}

// RetryAfter reads the provider's requested wait: retry-after-ms, else
// retry-after as seconds or an HTTP date against now. A missing, malformed,
// negative or over-MaxRetryAfter value is reported as absent.
func RetryAfter(h http.Header, now time.Time) (time.Duration, bool) {
	valid := func(d time.Duration) (time.Duration, bool) { return d, d >= 0 && d <= MaxRetryAfter }
	if v := strings.TrimSpace(h.Get("Retry-After-Ms")); v != "" {
		if ms, err := strconv.ParseFloat(v, 64); err == nil && ms >= 0 && ms <= float64(MaxRetryAfter/time.Millisecond) {
			return valid(time.Duration(ms * float64(time.Millisecond)))
		}
	}
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0, false
	}
	if s, err := strconv.ParseFloat(v, 64); err == nil {
		if s < 0 || s > MaxRetryAfter.Seconds() {
			return 0, false
		}
		return valid(time.Duration(s * float64(time.Second)))
	}
	if at, err := http.ParseTime(v); err == nil {
		d := at.Sub(now)
		if d < 0 {
			d = 0
		}
		return valid(d)
	}
	return 0, false
}

// RequestID is the provider's request id header, "" when none.
func RequestID(h http.Header) string {
	for _, k := range []string{"Request-Id", "X-Request-Id", "X-Ds-Trace-Id"} {
		if v := h.Get(k); v != "" {
			return v
		}
	}
	return ""
}

var errIdle = errors.New("the response body was idle too long")

// idleBody fails a read when no byte arrived for idle.
type idleBody struct {
	rc     io.ReadCloser
	cancel context.CancelCauseFunc
	parent context.Context
	ctx    context.Context
	timer  *time.Timer
	idle   time.Duration
	once   sync.Once
}

func (b *idleBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if n > 0 {
		b.timer.Reset(b.idle)
	}
	if err == nil || errors.Is(err, io.EOF) {
		return n, err
	}
	switch {
	case b.parent.Err() != nil:
		return n, context.Cause(b.parent)
	case errors.Is(context.Cause(b.ctx), errIdle):
		return n, fail(vocab.CodeTimeout, "no bytes for %v", b.idle)
	case egress.IsNotPermitted(err):
		return n, fail(vocab.CodeInvalidRequest, "%v", err)
	}
	return n, fail(vocab.CodeTransport, "the stream broke: %v", err)
}

func (b *idleBody) Close() error {
	err := b.rc.Close()
	b.once.Do(func() {
		b.timer.Stop()
		b.cancel(nil)
	})
	return err
}
