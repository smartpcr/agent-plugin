package launch

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// The Landlock filesystem access kinds (linux/landlock.h), by the interface
// version (ABI) that introduced them. They are spelled out here, not taken from
// golang.org/x/sys/unix, so the rule set is a pure function every lane can test.
const (
	AccessExecute    uint64 = 1 << 0 // ABI 1
	AccessWriteFile  uint64 = 1 << 1
	AccessReadFile   uint64 = 1 << 2
	AccessReadDir    uint64 = 1 << 3
	AccessRemoveDir  uint64 = 1 << 4
	AccessRemoveFile uint64 = 1 << 5
	AccessMakeChar   uint64 = 1 << 6
	AccessMakeDir    uint64 = 1 << 7
	AccessMakeReg    uint64 = 1 << 8
	AccessMakeSock   uint64 = 1 << 9
	AccessMakeFifo   uint64 = 1 << 10
	AccessMakeBlock  uint64 = 1 << 11
	AccessMakeSym    uint64 = 1 << 12
	AccessRefer      uint64 = 1 << 13 // ABI 2
	AccessTruncate   uint64 = 1 << 14 // ABI 3
	AccessIoctlDev   uint64 = 1 << 15 // ABI 5
)

// LandlockReference is the reference interface version (DH-ADR-NATIVESYSTEM-2):
// the stage asks for exactly the access kinds of min(kernel, reference), and a
// kernel below it is reported partial. Raising it is a reviewed change of the
// table below and of landlockGates.
const LandlockReference = 5

// landlockKinds are the filesystem access kinds each version adds, up to the
// reference. Version 4 added network kinds only, which this reference set does
// not request (they arrive with DH-ADR-SECURITY-10).
var landlockKinds = map[int]uint64{
	1: AccessExecute | AccessWriteFile | AccessReadFile | AccessReadDir | AccessRemoveDir | AccessRemoveFile |
		AccessMakeChar | AccessMakeDir | AccessMakeReg | AccessMakeSock | AccessMakeFifo | AccessMakeBlock | AccessMakeSym,
	2: AccessRefer,
	3: AccessTruncate,
	5: AccessIoctlDev,
}

// landlockGates are the effects a Landlock rule set governs by construction and
// the version each needs (the hand-kept list of DH-ADR-SANDBOX-3):
//
//   - harness-home: the home is outside every write root, so nothing grants a
//     write beneath it; truncation is governed from version 3, before which a
//     file in the home could still be truncated.
//   - fs-write: writes outside the roots are refused with every kind of the
//     reference set (truncation from 3, device ioctl from 5); a kernel below the
//     reference governs it only partly, so it is missing (DH-ADR-NATIVESYSTEM-2).
//
// network is not gated: this reference set requests no network kind, so a
// Landlock stage never governs it. delegated-execution is not a rule either; the
// stage reports it only when it finds no session service to reach.
var landlockGates = []struct {
	Effect sandbox.Effect
	ABI    int
}{
	{sandbox.EffectFSWrite, LandlockReference},
	{sandbox.EffectHarnessHome, 3},
}

// fileKinds are the kinds the kernel accepts on a rule for a non-directory.
const fileKinds = AccessExecute | AccessWriteFile | AccessReadFile | AccessTruncate | AccessIoctlDev

// readKinds are what every path keeps: read and execute (reads are not confined).
const readKinds = AccessExecute | AccessReadFile | AccessReadDir

// LandlockHandled is the handled access mask for a kernel at version abi: the
// kinds of min(abi, LandlockReference); zero below version 1.
func LandlockHandled(abi int) uint64 {
	var m uint64
	for v := 1; v <= min(abi, LandlockReference); v++ {
		m |= landlockKinds[v]
	}
	return m
}

// LandlockRule is one path-beneath rule: the access granted beneath Path. Dir is
// false for a path the rule grants only file kinds on.
type LandlockRule struct {
	Path   string
	Access uint64
	Dir    bool
}

// Ruleset is the rule set a Landlock stage applies to itself for one plan on one
// kernel, and what it governs by construction.
type Ruleset struct {
	// ABI is the kernel's interface version; Version is min(ABI, reference).
	ABI, Version int
	// Handled is the handled filesystem access mask (LandlockHandled(ABI)).
	Handled uint64
	// Rules are the grants, in order: read and execute on /, write on /dev/null,
	// then every handled kind beneath each write root.
	Rules []LandlockRule
	// Governed are the gated effects this kernel governs under the plan.
	Governed []sandbox.Effect
	// Missing are the gated effects this kernel's version lacks.
	Missing []sandbox.Effect
}

// Writable reports whether the rule set grants a write (WRITE_FILE or MAKE_REG)
// at path: true only beneath a write root, or at /dev/null itself.
func (r Ruleset) Writable(path string) bool {
	for _, rule := range r.Rules {
		if rule.Access&(AccessWriteFile|AccessMakeReg) == 0 {
			continue
		}
		if path == rule.Path || (rule.Dir && beneath(rule.Path, path)) {
			return true
		}
	}
	return false
}

// LandlockRules is the rule set of a confined plan p on a kernel at version abi
// (DH-ADR-NATIVESYSTEM-2): a version below 1 is refused, the handled kinds are
// those of min(abi, reference), writes are granted only beneath p.Write, and
// Governed/Missing follow landlockGates. harness-home is governed only when the
// plan names a home and no write root is it, contains it or lies inside it
// (Landlock is an allow-list: it cannot carve the home out of a root).
func LandlockRules(abi int, p Plan) (Ruleset, error) {
	if abi < 1 {
		return Ruleset{}, herr.Newf(sandbox.CodeUnavailable, "the kernel reports Landlock interface version %d; Landlock is not available", abi)
	}
	if p.Kind != Confined || p.Backend != sandbox.BackendLandlock {
		return Ruleset{}, failed("a Landlock rule set is for a confined landlock plan, not a %s", p)
	}
	handled := LandlockHandled(abi)
	rs := Ruleset{ABI: abi, Version: min(abi, LandlockReference), Handled: handled}
	rs.Rules = append(rs.Rules,
		LandlockRule{Path: "/", Access: readKinds & handled, Dir: true},
		LandlockRule{Path: "/dev/null", Access: (readKinds | AccessWriteFile | AccessTruncate | AccessIoctlDev) & fileKinds & handled})
	for _, w := range p.Write {
		rs.Rules = append(rs.Rules, LandlockRule{Path: w, Access: handled, Dir: true})
	}
	for _, g := range landlockGates {
		switch {
		case abi < g.ABI:
			rs.Missing = append(rs.Missing, g.Effect)
		case g.Effect == sandbox.EffectHarnessHome && !homeOutside(p.Home, p.Write):
		default:
			rs.Governed = append(rs.Governed, g.Effect)
		}
	}
	return rs, nil
}

// homeOutside reports whether home is named and no write root is it, contains it
// or lies inside it.
func homeOutside(home string, write []string) bool {
	return home != "" && !slices.ContainsFunc(write, func(w string) bool { return beneath(w, home) || beneath(home, w) })
}

// homeAboveRoots reports whether home is named and no write root is it or lies
// inside it: the home an outside confiner can overlay read-only after the roots
// without taking a root's writes away.
func homeAboveRoots(home string, write []string) bool {
	return home != "" && !slices.ContainsFunc(write, func(w string) bool { return beneath(home, w) })
}

// beneath reports whether path is root or below it.
func beneath(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
