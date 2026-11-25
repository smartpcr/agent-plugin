package onespawn

import "internal/world/subprocess"

// A test may build a Spec without a plan to prove the starter refuses it.
var unplannedInATest = subprocess.Spec{Argv: []string{"true"}}
