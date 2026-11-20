package llm

import (
	"context"
	"errors"
	"iter"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
)

// item is one value the pump hands to the normaliser.
type item struct {
	chunk vocab.Chunk
	err   error
	end   bool // the adapter's iterator returned
}

// Stream implements Prepared.
func (p *prepared) Stream(ctx context.Context, req session.Request, sink AttemptSink) iter.Seq[vocab.Chunk] {
	return func(yield func(vocab.Chunk) bool) {
		rec := Attempt{Route: p.route.ID, Model: p.model, RequestAsOf: req.AsOf(), RequestDigest: req.Digest()}
		rec.Timing.Start = p.rt.clock()
		if !req.IsZero() {
			rec.Purpose = req.Meta().Purpose
		}
		fin, asm := p.run(ctx, req, yield, &rec)
		p.settle(&rec, fin, asm)
		if fin.yield {
			yield(rec.Finish)
		}
		if sink != nil {
			_ = sink.Settle(rec) // the sink's error is the caller's; Complete reports it
		}
	}
}

// outcome is how the attempt ended before normalisation.
type outcome struct {
	finish vocab.Finish
	yield  bool // the consumer is still reading: the Finish is delivered
}

func abortedBy(ctx context.Context) *vocab.Failure {
	return failure(vocab.CodeAborted, "the attempt was cancelled: %v", context.Cause(ctx))
}

// run drives the adapter and forwards every non-finish chunk; it returns the
// raw finish and the assembled content.
func (p *prepared) run(ctx context.Context, req session.Request, yield func(vocab.Chunk) bool, rec *Attempt) (outcome, *assembler) {
	asm := newAssembler()
	fail := func(f *vocab.Failure) (outcome, *assembler) {
		return outcome{finish: vocab.Finish{Reason: vocab.FinishError, Failure: f}, yield: true}, asm
	}
	switch {
	case req.IsZero():
		return fail(failure(vocab.CodeInvalidRequest, "the request was never built"))
	case req.Route() != p.route.ID:
		return fail(failure(vocab.CodeInvalidRequest, "the request is for route %q, the call for %q", req.Route(), p.route.ID))
	}
	if err := ctx.Err(); err != nil {
		return outcome{finish: vocab.Finish{Reason: vocab.FinishAborted, Failure: abortedBy(ctx)}, yield: true}, asm
	}
	if p.slots != nil {
		select {
		case p.slots <- struct{}{}:
			defer func() { <-p.slots }()
		case <-ctx.Done():
			return outcome{finish: vocab.Finish{Reason: vocab.FinishAborted, Failure: abortedBy(ctx)}, yield: true}, asm
		}
	}

	actx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch := make(chan item)
	done := make(chan struct{})
	defer close(done)
	go p.pump(actx, req, ch, done)

	for {
		select {
		case <-ctx.Done():
			return outcome{finish: vocab.Finish{Reason: vocab.FinishAborted, Failure: abortedBy(ctx)}, yield: true}, asm
		case it := <-ch:
			switch {
			case it.err != nil:
				if ctx.Err() != nil {
					return outcome{finish: vocab.Finish{Reason: vocab.FinishAborted, Failure: abortedBy(ctx)}, yield: true}, asm
				}
				var f *vocab.Failure
				if errors.As(it.err, &f) {
					return fail(f)
				}
				return fail(failure(vocab.CodeTransport, "%v", it.err))
			case it.end:
				if ctx.Err() != nil {
					return outcome{finish: vocab.Finish{Reason: vocab.FinishAborted, Failure: abortedBy(ctx)}, yield: true}, asm
				}
				return fail(failure(vocab.CodeStreamClosed, "the stream ended without a finish"))
			}
			if f, ok := it.chunk.(vocab.Finish); ok {
				return outcome{finish: f, yield: true}, asm // nothing after a Finish is read
			}
			if it.chunk == nil {
				continue
			}
			if asm.add(it.chunk) && rec.Timing.FirstToken == 0 {
				rec.Timing.FirstToken = p.rt.clock()
			}
			if !yield(it.chunk) {
				return outcome{finish: vocab.Finish{Reason: vocab.FinishAborted,
					Failure: failure(vocab.CodeAborted, "the consumer stopped reading")}}, asm
			}
		}
	}
}

// pump runs the adapter's iterator on its own goroutine so a cancellation or a
// stalled provider never holds the normaliser; a panic is a malformed response.
func (p *prepared) pump(ctx context.Context, req session.Request, ch chan<- item, done <-chan struct{}) {
	send := func(it item) bool {
		select {
		case ch <- it:
			return true
		case <-done:
			return false
		}
	}
	defer func() {
		if r := recover(); r != nil {
			send(item{err: failure(vocab.CodeMalformedResponse, "the adapter panicked: %v", r)})
		}
	}()
	for c, err := range p.call.Attempt(ctx, req) {
		if err != nil {
			send(item{err: err})
			return
		}
		if !send(item{chunk: c}) {
			return
		}
		if _, ok := c.(vocab.Finish); ok {
			return
		}
	}
	send(item{end: true})
}

// settle normalises the finish and builds the record's message.
func (p *prepared) settle(rec *Attempt, o outcome, asm *assembler) {
	f := o.finish
	f.Replay = append([]byte(nil), f.Replay...)
	if len(f.Replay) == 0 {
		f.Replay = nil
	}
	switch f.Reason {
	case vocab.FinishStop, vocab.FinishToolCalls, vocab.FinishMaxTokens:
	case vocab.FinishAborted:
		if f.Failure == nil {
			f.Failure = failure(vocab.CodeAborted, "the provider aborted the attempt")
		}
	case vocab.FinishError:
		if f.Failure == nil {
			f.Failure = failure(vocab.CodeMalformedResponse, "the provider failed the attempt without a failure")
		}
	default:
		f = vocab.Finish{Reason: vocab.FinishError,
			Failure: failure(vocab.CodeMalformedResponse, "unknown finish reason %q", f.Reason)}
	}
	if f.Failure != nil && f.Reason != vocab.FinishAborted && f.Reason != vocab.FinishError {
		f.Reason = vocab.FinishError // a failure is never a normal finish
	}
	complete := f.Failure == nil
	blocks, ok := asm.blocks(complete, f.Reason == vocab.FinishMaxTokens)
	if !ok {
		f = vocab.Finish{Reason: vocab.FinishError,
			Failure: failure(vocab.CodeMalformedResponse, "a tool call has no id, no name or arguments that are not a JSON object")}
		blocks, _ = asm.blocks(false, false)
	}
	if f.Failure == nil && len(blocks) == 0 {
		f = vocab.Finish{Reason: vocab.FinishError, Failure: failure(vocab.CodeEmptyResponse, "the model returned no content")}
	}
	if len(blocks) > 0 {
		m, err := vocab.NewMessage(vocab.RoleAssistant, blocks...)
		if err != nil {
			f = vocab.Finish{Reason: vocab.FinishError, Failure: failure(vocab.CodeMalformedResponse, "%v", err)}
		} else {
			rec.Message = &m
		}
	}
	rec.Finish, rec.Usage = f, asm.usage
	rec.Timing.End = p.rt.clock()
}
