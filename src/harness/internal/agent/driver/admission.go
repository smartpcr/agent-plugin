// This file is point 8, admission (DH-ADR-AGENT-2, agent-loop.md §6.2): what a
// step's attempt adds to the log before its request is built, as one batch.

package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
)

// The source kinds of the messages admission writes for the prompt.
const (
	// SourcePrompt is the source kind of the system/message that carries the
	// rendered system prompt.
	SourcePrompt = "prompt"
	// SourcePromptContext is the source kind of a runtime-context snapshot, a
	// user/message whose source data names the context ({"name": ...}).
	SourcePromptContext = "prompt-context"
	// producer names the driver as the producer of a tool-change notice.
	producer = "agent.driver"
)

// maxAdmitTries bounds the retries of one admission whose tool-change notice was
// refused: once for a concurrent append that moved the header's position, once
// for a header the session folded as a new series.
const maxAdmitTries = 4

// admission is what point 8 admits for one attempt.
type admission struct {
	route    vocab.RouteID
	controls vocab.Controls
	// first is the step's first attempt: only it admits the system prompt, the
	// entered messages and the runtime contexts. A retry admits a changed header
	// only.
	first bool
	// series is Enter.StartsSeries: the first attempt's header is written even
	// when unchanged.
	series bool
	// entered is the step's batch as PreStep returned it, in its order; logged
	// holds the ids of those already in the log (a kept-open turn's insert).
	entered []vocab.Message
	logged  map[string]bool
}

// admitBatch is point 8: in one AppendGroup, the system/message when the
// prompt changed (the first at node 0; a later change replaces node 0), the
// entered messages, the runtime-context snapshots that changed, the
// request/header when it changed (or a series starts), and a developer/message
// naming the tools added and removed when the header continues a series with
// other tool names. An unchanged prompt adds nothing.
func (d *Driver) admitBatch(parent context.Context, ss *stepState, a admission) error {
	s := d.o.Session
	dropNotice := false
	for try := 1; ; try++ {
		base := s.Len()
		ds, notice, err := d.admissionDrafts(ss, a, base, dropNotice)
		if err != nil {
			return err
		}
		if len(ds) == 0 {
			return nil
		}
		err = d.write(parent, func(ctx context.Context) error {
			_, err := s.AppendGroup(ctx, ds)
			return err
		})
		if err == nil || !notice || !errors.Is(err, event.ErrInvalidSurface) || try >= maxAdmitTries {
			return err
		}
		// The notice cites its header by position. Nothing of the group was
		// written: either another writer appended first (the positions moved,
		// build again), or the session folded the header as a new series, which
		// declares every tool itself and needs no notice.
		if s.Len() == base {
			dropNotice = true
		}
	}
}

// admissionDrafts builds the batch for a log whose next position is base. notice
// reports a tool-change notice in it.
func (d *Driver) admissionDrafts(ss *stepState, a admission, base event.Seq, dropNotice bool) (ds []event.Draft, notice bool, err error) {
	s := d.o.Session
	cur, hasHead := s.RequestHeader()
	replaced := false
	if a.first {
		dr, rep, ok, err := d.promptDraft(ss, cur, hasHead)
		if err != nil {
			return nil, false, err
		}
		if ok {
			ds, replaced = append(ds, dr), rep
		}
		for _, m := range a.entered {
			if a.logged[m.ID()] {
				continue
			}
			dr, err := messageDraft(m)
			if err != nil {
				return nil, false, err
			}
			ds = append(ds, dr)
		}
		cds, err := d.contextDrafts(ss.asm.Contexts)
		if err != nil {
			return nil, false, err
		}
		ds = append(ds, cds...)
	}
	hp := headerPayload(a.route, ss.asm, a.controls)
	if !(a.series && a.first) && !replaced && hasHead && sameHeader(cur, hp) {
		return ds, false, nil
	}
	hdr, err := event.RequestHeader.Draft(hp)
	if err != nil {
		return nil, false, err
	}
	at := base + event.Seq(len(ds))
	ds = append(ds, hdr)
	if dropNotice || !hasHead || replaced {
		return ds, false, nil
	}
	th := s.ToolHistory()
	if !th.HasBase || restartsSeries(th, cur, hp.Tools) {
		return ds, false, nil
	}
	added, removed := toolChange(th.Active(), hp.Tools)
	if len(added) == 0 && len(removed) == 0 {
		return ds, false, nil
	}
	nd, err := toolNotice(at, added, removed)
	if err != nil {
		return nil, false, err
	}
	return append(ds, nd), true, nil
}

// promptDraft is the step's system/message, when the prompt changed: ok is
// false for an empty prompt, for a header in force with the assembly's
// fingerprint, and for a node 0 that already holds this text. A system/message
// at node 0 is replaced (replaced true), which starts a new request series;
// otherwise the prompt is appended, at node 0 when the history is empty.
func (d *Driver) promptDraft(ss *stepState, cur surface.RequestHeader, hasHead bool) (dr event.Draft, replaced, ok bool, err error) {
	if ss.system == "" || (hasHead && cur.Fingerprint != "" && cur.Fingerprint == ss.asm.Fingerprint) {
		return event.Draft{}, false, false, nil
	}
	hist := d.o.Session.History()
	var node0 *surface.Node
	if hist.Len() > 0 && hist.At(0).Type() == event.SystemMessage.Name() {
		n := hist.At(0)
		if m, err := n.Message(); err == nil && textOf(m) == ss.system {
			return event.Draft{}, false, false, nil
		}
		node0 = &n
	}
	msg, err := vocab.NewMessage(vocab.RoleSystem, vocab.TextBlock(ss.system))
	if err != nil {
		return event.Draft{}, false, false, err
	}
	if msg, err = msg.WithSource(vocab.Source{Kind: SourcePrompt, Producers: owners(ss.asm)}); err != nil {
		return event.Draft{}, false, false, err
	}
	p := event.MessagePayload{Message: msg}
	if node0 != nil {
		at := node0.Seq()
		dr, err = event.SystemMessage.Draft(event.Replace(at, at), p, at)
		return dr, true, err == nil, err
	}
	dr, err = event.SystemMessage.Draft(event.Append(), p)
	return dr, false, err == nil, err
}

// owners are the distinct owners of the assembly's sections, in order.
func owners(a prompt.Assembly) []string {
	var out []string
	for _, s := range a.Sections {
		if s.Owner != "" && !slices.Contains(out, s.Owner) {
			out = append(out, s.Owner)
		}
	}
	if len(out) == 0 {
		out = []string{prompt.Name}
	}
	return out
}

func textOf(m vocab.Message) string {
	var b strings.Builder
	for _, bl := range m.Blocks() {
		if bl.Kind == vocab.KindText && bl.Text != nil {
			b.WriteString(bl.Text.Text)
		}
	}
	return b.String()
}

// contextDrafts are the runtime-context snapshots whose text differs from the
// latest snapshot of the same name in the history, each a user/message with
// source prompt-context.
func (d *Driver) contextDrafts(snaps []prompt.Snapshot) ([]event.Draft, error) {
	if len(snaps) == 0 {
		return nil, nil
	}
	last := latestContexts(d.o.Session.History(), len(snaps))
	var out []event.Draft
	for _, sn := range snaps {
		if t, ok := last[sn.Name]; ok && t == sn.Text {
			continue
		}
		data, err := json.Marshal(map[string]string{"name": sn.Name})
		if err != nil {
			return nil, err
		}
		owner := sn.Owner
		if owner == "" {
			owner = prompt.Name
		}
		m, err := vocab.NewMessage(vocab.RoleUser, vocab.TextBlock(sn.Text))
		if err != nil {
			return nil, err
		}
		if m, err = m.WithSource(vocab.Source{Kind: SourcePromptContext, Producers: []string{owner}, Data: data}); err != nil {
			return nil, err
		}
		dr, err := event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: m})
		if err != nil {
			return nil, err
		}
		out = append(out, dr)
	}
	return out, nil
}

// latestContexts is the text of the latest snapshot of each runtime context in
// h, read from the end, stopping once want names are found.
func latestContexts(h surface.History, want int) map[string]string {
	out := map[string]string{}
	for i := h.Len() - 1; i >= 0 && len(out) < want; i-- {
		n := h.At(i)
		if n.Role() != vocab.RoleUser || !strings.Contains(n.Raw(), `"`+SourcePromptContext+`"`) {
			continue
		}
		m, err := n.Message()
		if err != nil {
			continue
		}
		src, ok := m.Source()
		if !ok || src.Kind != SourcePromptContext {
			continue
		}
		var data struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(src.Data, &data) != nil || data.Name == "" {
			continue
		}
		if _, seen := out[data.Name]; !seen {
			out[data.Name] = textOf(m)
		}
	}
	return out
}

// messageDraft is m as the event of its role.
func messageDraft(m vocab.Message) (event.Draft, error) {
	switch m.Role() {
	case vocab.RoleUser:
		return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: m})
	case vocab.RoleDeveloper:
		return event.DeveloperMessage.Draft(event.Append(), event.MessagePayload{Message: m})
	case vocab.RoleSystem:
		return event.SystemMessage.Draft(event.Append(), event.MessagePayload{Message: m})
	}
	return event.Draft{}, fmt.Errorf("driver: cannot insert a %s message", m.Role())
}

// headerPayload is the request/header of a step on route: the assembly's tools
// in its order (toolOrder), fingerprint and manifest, and the controls.
func headerPayload(route vocab.RouteID, a prompt.Assembly, c vocab.Controls) event.RequestHeaderPayload {
	p := event.RequestHeaderPayload{Route: route, Tools: a.Tools, Fingerprint: a.Fingerprint}
	for _, m := range a.Manifest() {
		p.Manifest = append(p.Manifest, event.ManifestEntry(m))
	}
	if !reflect.DeepEqual(c, vocab.Controls{}) {
		ctrl := c.Clone()
		p.Controls = &ctrl
	}
	return p
}

// sameHeader reports that p would change nothing of the header in force.
func sameHeader(cur surface.RequestHeader, p event.RequestHeaderPayload) bool {
	c := vocab.Controls{}
	if p.Controls != nil {
		c = *p.Controls
	}
	return cur.Route == p.Route && sameTools(cur.Tools, p.Tools) && reflect.DeepEqual(cur.Controls, c) &&
		cur.Fingerprint == p.Fingerprint && slices.Equal(cur.Manifest, p.Manifest)
}

// sameTools compares two tool lists by name, description and the canonical
// encoding of their parameter schemas: the header in force holds the schema as
// the log stored it (keys sorted), a declaration as it was derived, so their raw
// bytes differ for the same schema (M1-G-LOOP-4).
func sameTools(a, b []vocab.ToolSchema) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Description != b[i].Description || !sameSchema(a[i].Parameters, b[i].Parameters) {
			return false
		}
	}
	return true
}

func sameSchema(a, b json.RawMessage) bool {
	if bytes.Equal(a, b) {
		return true
	}
	ca, err := vocab.Canonical(a)
	if err != nil {
		return false
	}
	cb, err := vocab.Canonical(b)
	return err == nil && bytes.Equal(ca, cb)
}

// restartsSeries reports that the session will fold a header with tools as a new
// series: it redefines a name the series knows (from its base, its additions or
// the header in force).
func restartsSeries(th surface.ToolHistory, cur surface.RequestHeader, ts []vocab.ToolSchema) bool {
	known := map[string]string{}
	note := func(list []vocab.ToolSchema) {
		for _, t := range list {
			if d, err := vocab.Digest(t); err == nil {
				known[t.Name] = d
			}
		}
	}
	note(th.Declared)
	for _, c := range th.Changes {
		note(c.Added)
	}
	note(cur.Tools)
	for _, t := range ts {
		d, err := vocab.Digest(t)
		if err != nil {
			return true
		}
		if k, ok := known[t.Name]; ok && k != d {
			return true
		}
	}
	return false
}

// toolChange is the names ts adds to active, in ts's order, and the names it
// removes, in active's order.
func toolChange(active, ts []vocab.ToolSchema) (added, removed []string) {
	in := func(list []vocab.ToolSchema, name string) bool {
		return slices.ContainsFunc(list, func(t vocab.ToolSchema) bool { return t.Name == name })
	}
	for _, t := range ts {
		if !in(active, t.Name) {
			added = append(added, t.Name)
		}
	}
	for _, t := range active {
		if !in(ts, t.Name) {
			removed = append(removed, t.Name)
		}
	}
	return added, removed
}

// toolNotice is the developer/message telling the model which tools were added
// and removed, as a tool update citing header.
func toolNotice(header event.Seq, added, removed []string) (event.Draft, error) {
	data, err := json.Marshal(surface.ToolUpdate{Header: header, Added: added, Removed: removed})
	if err != nil {
		return event.Draft{}, err
	}
	text := "The tools available to you changed."
	if len(added) > 0 {
		text += " Added: " + strings.Join(added, ", ") + "."
	}
	if len(removed) > 0 {
		text += " Removed: " + strings.Join(removed, ", ") + "."
	}
	m, err := vocab.NewMessage(vocab.RoleDeveloper, vocab.TextBlock(text))
	if err != nil {
		return event.Draft{}, err
	}
	if m, err = m.WithSource(vocab.Source{Kind: surface.ToolUpdateKind, Producers: []string{producer}, Data: data}); err != nil {
		return event.Draft{}, err
	}
	return event.DeveloperMessage.Draft(event.Append(), event.MessagePayload{Message: m})
}

// assemble is point 4's prompt: the assembly of the step's view and its
// rendered system prompt.
func (d *Driver) assemble(ctx context.Context, view tools.StepView) (prompt.Assembly, string, error) {
	a, err := d.o.Prompt.Assemble(ctx, prompt.Input{View: view})
	if err != nil {
		return prompt.Assembly{}, "", err
	}
	text, err := d.o.Prompt.Render(a)
	if err != nil {
		return prompt.Assembly{}, "", err
	}
	return a, text, nil
}
