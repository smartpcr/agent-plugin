package headless

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
)

// ChannelCLI is the answering channel of the terminal that started the run.
const ChannelCLI human.Channel = "cli"

// peerTerminal is the peer of every answer the terminal gives.
const peerTerminal human.PeerRef = "terminal"

// terminal is the `cli` answerer (DH-ADR-CLI-7): it writes each approval to the
// diagnostic stream and reads the answer, one line, from the controlling
// terminal, never from a piped standard input. Prompts are asked one at a time.
// Questions are not answered here: delivering one fails, so it settles
// unavailable.
type terminal struct {
	reg  human.Registry
	in   io.Reader
	out  io.Writer
	once sync.Once
	// lines are the terminal's lines, read only once the first prompt is shown.
	lines chan string
	mu    sync.Mutex
}

func newTerminal(reg human.Registry, in io.Reader, out io.Writer) *terminal {
	return &terminal{reg: reg, in: in, out: out, lines: make(chan string)}
}

func (t *terminal) read() {
	s := bufio.NewScanner(t.in)
	for s.Scan() {
		t.lines <- s.Text()
	}
	close(t.lines)
}

func (t *terminal) Deliver(ctx context.Context, it human.Item) error {
	var p approval.Prompt
	switch v := it.Payload.(type) {
	case approval.Prompt:
		p = v
	case *approval.Prompt:
		p = *v
	default:
		return fmt.Errorf("the terminal answers approvals only, not %s", it.Kind)
	}
	go t.ask(ctx, it.ID, p)
	return nil
}

func (t *terminal) Withdraw(human.ID, human.Reason) {}

// ask shows p and answers id from the next terminal line: y allows once, s
// allows with a session grant (offered only for a request naming a subject),
// anything else denies, and a closed terminal denies. A request settled
// meanwhile (ctx ends) is not answered.
func (t *terminal) ask(ctx context.Context, id human.ID, p approval.Prompt) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if ctx.Err() != nil {
		return
	}
	t.once.Do(func() { go t.read() })
	what := p.Tool
	if p.Display != "" {
		what += " " + p.Display
	}
	if p.Reason != "" {
		what += " (" + p.Reason + ")"
	}
	choices := "[y] allow once, [n] deny"
	if p.Subject != nil {
		choices = "[y] allow once, [s] allow for this session, [n] deny"
	}
	fmt.Fprintf(t.out, "dsh headless: approve %s? %s: ", strings.Join(strings.Fields(what), " "), choices)
	a := human.Answer{Outcome: human.OutcomeDenied, Text: "denied on the terminal"}
	select {
	case line, ok := <-t.lines:
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			a = human.Answer{Outcome: human.OutcomeAllowedOnce}
		case "s", "session":
			if p.Subject != nil {
				a = human.Answer{Outcome: human.OutcomeAllowedSession}
			}
		}
		if !ok {
			a.Text = "the terminal closed"
		}
	case <-ctx.Done():
		fmt.Fprintln(t.out)
		return
	}
	_ = t.reg.Answer(context.WithoutCancel(ctx), human.Actor{Channel: ChannelCLI, Name: string(ChannelCLI), Peer: peerTerminal}, id, a)
}
