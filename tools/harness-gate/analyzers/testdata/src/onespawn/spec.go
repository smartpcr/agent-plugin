package onespawn

import "internal/world/subprocess"

// Planned names its plan.
var Planned = subprocess.Spec{Argv: []string{"true"}, Launch: subprocess.Launch{Reason: "fixture"}}

// Unplanned does not.
var Unplanned = subprocess.Spec{Argv: []string{"true"}} // want `subprocess.Spec without Launch`

// Empty is the zero Spec.
var Empty = &subprocess.Spec{} // want `subprocess.Spec without Launch`

// Elided is a list whose element type is elided.
var Elided = []subprocess.Spec{{Argv: []string{"true"}}} // want `subprocess.Spec without Launch`

// Other is a Spec-like type of another package.
type Other struct{ Argv []string }

// CommandSpec is an alias of the spawn package's Spec: the same type.
type CommandSpec = subprocess.Spec

// Aliased names the Spec through the alias and sets no plan.
var Aliased = CommandSpec{Argv: []string{"true"}} // want `subprocess.Spec without Launch`

// AliasedPlanned names the Spec through the alias with its plan.
var AliasedPlanned = CommandSpec{Argv: []string{"true"}, Launch: subprocess.Launch{Reason: "fixture"}}

// NotSpawn is not the spawn package's Spec.
var NotSpawn = Other{Argv: []string{"true"}}
