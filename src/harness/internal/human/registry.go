package human

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
)

// maxVia bounds a Via chain; a longer one (or a cycle) has no root.
const maxVia = 16

func (r *registry) RegisterAnswerer(owner *cordis.Context, ch Channel, a Answerer) (*cordis.EffectHandle, error) {
	if owner == nil || ch == "" || a == nil {
		return nil, errorf(CodeInvalid, "RegisterAnswerer needs an owner, a channel and an answerer")
	}
	return owner.Effect("human.answerer:"+string(ch), func() (cordis.Disposer, error) {
		r.mu.Lock()
		if _, taken := r.answerers[ch]; taken {
			r.mu.Unlock()
			return nil, errorf(CodeTaken, "channel %q already has an answerer", ch)
		}
		r.gen++
		gen := r.gen
		r.answerers[ch] = answerer{a: a, gen: gen}
		r.mu.Unlock()
		return func() error {
			r.mu.Lock()
			if cur, ok := r.answerers[ch]; ok && cur.gen == gen {
				delete(r.answerers, ch)
			}
			r.mu.Unlock()
			r.settleWhere(func(x *item) bool { return x.channel == ch && x.ansGen == gen }, "answerer-unloaded")
			return nil
		}, nil
	})
}

func (r *registry) Bind(scope *cordis.Context, ref agent.Ref, b Binding) (*cordis.EffectHandle, error) {
	if scope == nil || ref.IsZero() || (b.Channel == "") == b.Via.IsZero() {
		return nil, errorf(CodeInvalid, "Bind needs a scope, an agent and exactly one of a channel or Via")
	}
	if b.Via == ref {
		return nil, errorf(CodeInvalid, "%s cannot be bound via itself", ref)
	}
	return scope.Effect("human.binding:"+ref.String(), func() (cordis.Disposer, error) {
		r.mu.Lock()
		if _, taken := r.bindings[ref]; taken {
			r.mu.Unlock()
			return nil, errorf(CodeTaken, "%s is already bound", ref)
		}
		r.gen++
		gen := r.gen
		r.bindings[ref] = binding{b: b, gen: gen}
		r.mu.Unlock()
		return func() error {
			r.mu.Lock()
			if cur, ok := r.bindings[ref]; ok && cur.gen == gen {
				delete(r.bindings, ref)
			}
			r.mu.Unlock()
			r.settleWhere(func(x *item) bool { return slices.Contains(x.path, ref) }, "unbound")
			return nil
		}, nil
	})
}

// rootLocked follows ref's Via chain to the bound channel; path is every agent
// on the way, ref first.
func (r *registry) rootLocked(ref agent.Ref) (ch Channel, path []agent.Ref, ok bool) {
	for range maxVia {
		path = append(path, ref)
		b, bound := r.bindings[ref]
		if !bound {
			return "", path, false
		}
		if b.b.Channel != "" {
			return b.b.Channel, path, true
		}
		ref = b.b.Via
	}
	return "", path, false
}

func unavailable(detail string) (Answer, Actor, error) {
	return Answer{Outcome: OutcomeUnavailable, Detail: detail}, Actor{}, nil
}

func (r *registry) Ask(ctx context.Context, it Item) (Answer, Actor, error) {
	if it.Kind != KindApproval && it.Kind != KindQuestion {
		return Answer{}, Actor{}, errorf(CodeInvalid, "kind %q is not approval or question", it.Kind)
	}
	if it.To.Agent.IsZero() {
		return Answer{}, Actor{}, errorf(CodeInvalid, "an interaction needs its agent")
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return unavailable("host-stopping")
	}
	ch, path, ok := r.rootLocked(it.To.Agent)
	if !ok {
		r.mu.Unlock()
		return unavailable("unbound")
	}
	a, ok := r.answerers[ch]
	if !ok {
		r.mu.Unlock()
		return unavailable("no-answerer")
	}
	if it.ID == "" {
		// A minted id must not collide with a caller-supplied one, pending or settled.
		for {
			r.seq++
			it.ID = ID("int-" + strconv.FormatUint(r.seq, 10))
			if _, dup := r.items[it.ID]; !dup && !r.isSettled(it.ID) {
				break
			}
		}
	} else if _, dup := r.items[it.ID]; dup || r.isSettled(it.ID) {
		r.mu.Unlock()
		return Answer{}, Actor{}, errorf(CodeInvalid, "item %q is already used", it.ID)
	} else {
		r.seq++
	}
	if !it.Deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, it.Deadline)
		defer cancel()
	}
	// The answerer's context is the item's own: settling the item by any path
	// (answer, unbind, answerer unload, Close, cancellation) cancels it.
	dctx, dcancel := context.WithCancel(ctx)
	defer dcancel()
	x := &item{it: it, seq: r.seq, channel: ch, ans: a.a, ansGen: a.gen, path: path, done: make(chan struct{}), cancel: dcancel}
	r.items[it.ID] = x
	r.mu.Unlock()

	if err := deliver(dctx, a.a, it); err != nil {
		r.settle(x, Answer{Outcome: OutcomeUnavailable, Detail: "answerer-failed: " + err.Error()}, Actor{})
	}
	select {
	case <-x.done:
	case <-ctx.Done():
		r.settle(x, Answer{Outcome: OutcomeCancelled, Detail: context.Cause(ctx).Error()}, Actor{})
		<-x.done
	}
	return x.res, x.by, nil
}

func deliver(ctx context.Context, a Answerer, it Item) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("the answerer panicked: %v", p)
		}
	}()
	return a.Deliver(ctx, it)
}

func withdraw(a Answerer, id ID, why Reason) {
	defer func() { _ = recover() }() // a carrier's failure to withdraw changes nothing
	a.Withdraw(id, why)
}

// settle is the one compare-and-swap that settles x; false when x had settled.
func (r *registry) settle(x *item, a Answer, by Actor) bool {
	if !x.state.CompareAndSwap(pending, settled) {
		return false
	}
	x.res, x.by = a, by
	r.mu.Lock()
	if r.items[x.it.ID] == x {
		delete(r.items, x.it.ID)
		r.settled[x.it.ID] = struct{}{}
	}
	r.mu.Unlock()
	close(x.done)
	x.cancel()
	withdraw(x.ans, x.it.ID, Reason(a.Outcome))
	return true
}

func (r *registry) settleWhere(match func(*item) bool, detail string) {
	r.mu.Lock()
	var hit []*item
	for _, x := range r.items {
		if match(x) {
			hit = append(hit, x)
		}
	}
	r.mu.Unlock()
	for _, x := range hit {
		r.settle(x, Answer{Outcome: OutcomeUnavailable, Detail: detail}, Actor{})
	}
}

func validAnswer(k Kind, a Answer) bool {
	switch k {
	case KindApproval:
		return a.Outcome == OutcomeAllowedOnce || a.Outcome == OutcomeAllowedSession || a.Outcome == OutcomeDenied
	case KindQuestion:
		return a.Outcome == OutcomeAnswered && a.Text != "" || a.Outcome == OutcomeDenied
	}
	return false
}

func (r *registry) Answer(_ context.Context, by Actor, id ID, a Answer) error {
	r.mu.Lock()
	x, ok := r.items[id]
	_, known := r.settled[id]
	r.mu.Unlock()
	switch {
	case !ok && known:
		return errorf(CodeSettled, "item %q has settled", id)
	case !ok:
		return errorf(CodeUnknown, "no item %q", id)
	case by.Channel != x.channel:
		return errorf(CodeNotYours, "item %q is answered on %q, not %q", id, x.channel, by.Channel)
	case !validAnswer(x.it.Kind, a):
		return errorf(CodeInvalid, "%q is not an answer to a %s", a.Outcome, x.it.Kind)
	}
	if !r.settle(x, Answer{Outcome: a.Outcome, Text: a.Text}, by) {
		return errorf(CodeSettled, "item %q has settled", id)
	}
	return nil
}

func (r *registry) isSettled(id ID) bool { _, ok := r.settled[id]; return ok }

func (r *registry) list(match func(*item) bool) []Item {
	r.mu.Lock()
	var xs []*item
	for _, x := range r.items {
		if match(x) {
			xs = append(xs, x)
		}
	}
	r.mu.Unlock()
	slices.SortFunc(xs, func(a, b *item) int { return int(a.seq) - int(b.seq) })
	out := make([]Item, len(xs))
	for i, x := range xs {
		out[i] = x.it
	}
	return out
}

func (r *registry) Pending(ref agent.Ref) []Item {
	return r.list(func(x *item) bool { return x.it.To.Agent == ref })
}

func (r *registry) PendingAll() []Item { return r.list(func(*item) bool { return true }) }

func (r *registry) Close() {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	r.settleWhere(func(*item) bool { return true }, "host-stopping")
}
