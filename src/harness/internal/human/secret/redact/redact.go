// Package redact is the component human.secret.redact (DH-D-HUMAN-7, SP-11): a
// shaper on the tool registry that masks, in every tool result's text, each value
// the secret store has released. A tool that echoes a resolved secret, or an error
// that quotes one, reaches the log and the model as "[redacted]".
//
// The shaper is filed as a final shaper (tools.FinalShaper): it runs after every
// ordinary shaper, whenever that one was registered, on the text they left (a
// Block's text included), and before the spill seat, so a saved full result is
// masked too. It cannot mask a value the store never released, such as a secret a
// tool read by itself.
package redact

import (
	"errors"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// Name is the component's catalogue name, and the shaper's id in results.
const Name = "human.secret.redact"

// Shaper masks every value store has released in a call's text (Store.Mask). A
// text with nothing to mask is accepted as it is.
func Shaper(store *secret.Store) tools.Shaper {
	return func(_ tools.Call, o tools.Outcome) tools.PostDecision {
		if masked := store.MaskString(o.Text); masked != o.Text {
			return tools.ReplaceContent(masked)
		}
		return tools.Accept()
	}
}

// Mount files the shaper on svc as a final shaper in the layer of ctx's scope, as
// an effect of ctx's fiber. A registry that cannot run a final shaper is refused
// rather than given an ordinary one, which a later shaper could undo.
func Mount(ctx *cordis.Context, svc tools.Service, store *secret.Store) (*cordis.EffectHandle, error) {
	if svc == nil || store == nil {
		return nil, errors.New("redact: the tool registry and the secret store are required")
	}
	final, ok := svc.(tools.FinalShaper)
	if !ok {
		return nil, errors.New("redact: the tool registry cannot run a final shaper, so the mask could be undone")
	}
	return final.ShapeFinal(ctx, Name, Shaper(store))
}

// Component is human.secret.redact: it injects the tool registry and the secret
// store and mounts the shaper for as long as its fiber is loaded.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	svc, err := tools.Key.Get(ctx)
	if err != nil {
		return err
	}
	store, err := secret.Key.Get(ctx)
	if err != nil {
		return err
	}
	_, err = Mount(ctx, svc, store)
	return err
}, cordis.WithInject(tools.Key.Name(), secret.Key.Name()))
