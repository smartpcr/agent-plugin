package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Agent is one research agent and the upstream units it must give a disposition to.
type Agent struct {
	Key     string   `json:"key"`     // file name of its fragment, for example "A04"
	Wave    int      `json:"wave"`    // a later wave may override an earlier wave's row
	Kind    string   `json:"kind"`    // foundation | decisions | library | area | audit
	IDKey   string   `json:"idKey"`   // namespace for the DH- IDs it creates, for example "LLM"
	Title   string   `json:"title"`   // what the agent covers
	Paths   []string `json:"paths"`   // upstream path prefixes it owns
	Deps    []string `json:"deps"`    // dependency names it owns; a trailing * is a prefix match
	Outputs []string `json:"outputs"` // documents it writes, relative to docs/harness
}

// Clusters is docs/harness/trace/clusters.json: who owns what.
type Clusters struct {
	Agents         []Agent           `json:"agents"`
	DecAgents      []string          `json:"decAgents"`      // decision notes are split evenly between these
	DebtKindOwners map[string]string `json:"debtKindOwners"` // DEBT kind -> agent
	TypeOwners     map[string]string `json:"typeOwners"`     // unit type -> agent, used before the path rule
	TypeFallback   map[string]string `json:"typeFallback"`   // unit type -> agent, used when no path matches
	DepFallback    map[string]string `json:"depFallback"`    // DEP kind -> agent, used when no name matches
}

func loadClusters(c config) (*Clusters, error) {
	b, err := os.ReadFile(filepath.Join(c.trace, "clusters.json"))
	if err != nil {
		return nil, err
	}
	var cl Clusters
	if err := json.Unmarshal(b, &cl); err != nil {
		return nil, fmt.Errorf("clusters.json: %w", err)
	}
	seen := map[string]bool{}
	ids := map[string]string{}
	for _, a := range cl.Agents {
		if seen[a.Key] {
			return nil, fmt.Errorf("clusters.json: agent %s is listed twice", a.Key)
		}
		seen[a.Key] = true
		if a.IDKey != "" {
			if other, dup := ids[a.IDKey]; dup {
				return nil, fmt.Errorf("clusters.json: idKey %s is used by %s and %s", a.IDKey, other, a.Key)
			}
			ids[a.IDKey] = a.Key
		}
	}
	return &cl, nil
}

func (cl *Clusters) agent(key string) *Agent {
	for i := range cl.Agents {
		if cl.Agents[i].Key == key {
			return &cl.Agents[i]
		}
	}
	return nil
}

func depMatches(pattern, name string) bool {
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(name, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == name
}

// ownerByDep returns the agent that lists the dependency name. An exact match wins
// over a prefix match.
func (cl *Clusters) ownerByDep(name string) (string, error) {
	var exact, prefix []string
	for _, a := range cl.Agents {
		for _, p := range a.Deps {
			if p == name {
				exact = append(exact, a.Key)
			} else if depMatches(p, name) {
				prefix = append(prefix, a.Key)
			}
		}
	}
	for _, hits := range [][]string{exact, prefix} {
		if len(hits) > 1 {
			return "", fmt.Errorf("dependency %s is claimed by %s", name, strings.Join(hits, ", "))
		}
		if len(hits) == 1 {
			return hits[0], nil
		}
	}
	return "", nil
}

// ownerByPath returns the agent with the longest matching path prefix.
func (cl *Clusters) ownerByPath(p string) (string, error) {
	best, bestLen := "", -1
	tie := ""
	for _, a := range cl.Agents {
		for _, pre := range a.Paths {
			if pre == "." || p == pre || strings.HasPrefix(p, pre+"/") {
				n := len(pre)
				if pre == "." {
					n = 0
				}
				if n > bestLen {
					best, bestLen, tie = a.Key, n, ""
				} else if n == bestLen && a.Key != best {
					tie = a.Key
				}
			}
		}
	}
	if tie != "" {
		return "", fmt.Errorf("path %s is claimed by %s and %s", p, best, tie)
	}
	return best, nil
}

// assignUnits decides the owner of every unit. It returns the owner of each unit ID
// and the list of units nobody owns.
func assignUnits(cl *Clusters, units []Unit) (map[string]string, []string, error) {
	owner := map[string]string{}
	var unassigned []string

	// Decision notes are split evenly, in category order, so each batch is coherent.
	var decs []Unit
	for _, u := range units {
		if u.Type == "DEC" {
			decs = append(decs, u)
		}
	}
	sort.Slice(decs, func(i, j int) bool {
		ci, cj := fmt.Sprint(decs[i].Attrs["category"]), fmt.Sprint(decs[j].Attrs["category"])
		if ci != cj {
			return ci < cj
		}
		return decs[i].Name < decs[j].Name
	})
	if n := len(cl.DecAgents); n > 0 {
		per := (len(decs) + n - 1) / n
		for i, u := range decs {
			owner[u.ID] = cl.DecAgents[i/per]
		}
	}

	for _, u := range units {
		if u.Type == "DEC" {
			if owner[u.ID] == "" {
				unassigned = append(unassigned, u.ID)
			}
			continue
		}
		kind := fmt.Sprint(u.Attrs["kind"])
		key := ""
		var err error
		switch {
		case u.Type == "DEBT" && cl.DebtKindOwners[kind] != "":
			key = cl.DebtKindOwners[kind]
		case u.Type == "DEP" || (u.Type == "DEBT" && (kind == "patch" || kind == "pin")):
			name := u.Name
			if u.Type == "DEBT" {
				name = fmt.Sprint(u.Attrs["dep"])
			}
			if key, err = cl.ownerByDep(name); err != nil {
				return nil, nil, err
			}
			if key == "" && u.Type == "DEP" && u.Path != "" {
				if key, err = cl.ownerByPath(u.Path); err != nil {
					return nil, nil, err
				}
			}
			if key == "" {
				if u.Type == "DEP" {
					key = cl.DepFallback[kind]
				} else {
					key = cl.DepFallback["debt"]
				}
			}
		case cl.TypeOwners[u.Type] != "":
			key = cl.TypeOwners[u.Type]
		default:
			if u.Path != "" {
				if key, err = cl.ownerByPath(u.Path); err != nil {
					return nil, nil, err
				}
			}
			if key == "" {
				key = cl.TypeFallback[u.Type]
			}
		}
		if key == "" || cl.agent(key) == nil {
			unassigned = append(unassigned, u.ID)
			continue
		}
		owner[u.ID] = key
	}
	return owner, unassigned, nil
}

func runAssign(c config) error {
	cl, err := loadClusters(c)
	if err != nil {
		return err
	}
	units, err := loadInventory(c)
	if err != nil {
		return err
	}
	owner, unassigned, err := assignUnits(cl, units)
	if err != nil {
		return err
	}
	per := map[string][]Unit{}
	for _, u := range units {
		if k := owner[u.ID]; k != "" {
			per[k] = append(per[k], u)
		}
	}
	dir := filepath.Join(c.trace, "assignments")
	_ = os.RemoveAll(dir)
	fmt.Printf("%-5s %-4s %-12s %6s %7s  %s\n", "agent", "wave", "idKey", "units", "srcKB", "by type")
	for _, a := range cl.Agents {
		rows := per[a.Key]
		if err := writeJSONL(filepath.Join(dir, a.Key+".jsonl"), rows); err != nil {
			return err
		}
		counts := map[string]int{}
		var src float64
		for _, u := range rows {
			counts[u.Type]++
			if u.Type == "PKG" {
				if v, ok := u.Attrs["srcBytes"].(float64); ok {
					src += v
				}
			}
		}
		var parts []string
		for _, t := range unitTypes {
			if counts[t] > 0 {
				parts = append(parts, fmt.Sprintf("%s=%d", t, counts[t]))
			}
		}
		fmt.Printf("%-5s %-4d %-12s %6d %7.0f  %s\n", a.Key, a.Wave, a.IDKey, len(rows), src/1024, strings.Join(parts, " "))
	}
	fmt.Printf("assigned %d of %d units\n", len(owner), len(units))
	if len(unassigned) > 0 {
		sort.Strings(unassigned)
		fmt.Printf("UNASSIGNED %d:\n", len(unassigned))
		for i, id := range unassigned {
			if i == 60 {
				fmt.Printf("  ... and %d more\n", len(unassigned)-60)
				break
			}
			fmt.Println("  ", id)
		}
		return fmt.Errorf("%d units have no owner", len(unassigned))
	}
	return nil
}

// DecHandoff is a decision note passed from a decision agent to the area agent that
// owns the code the decision is about. The area agent writes the final disposition.
type DecHandoff struct {
	Unit         string `json:"unit"`
	Title        string `json:"title"`
	Note         string `json:"note"`
	Summary      string `json:"summary"`
	FirstVerdict string `json:"firstVerdict"`
	FirstReason  string `json:"firstReason"`
	ClassifiedBy string `json:"classifiedBy"`
}

// runHandoff routes each classified decision note to the later-wave agent that owns
// the upstream path the note is about.
func runHandoff(c config) error {
	cl, err := loadClusters(c)
	if err != nil {
		return err
	}
	units, err := loadInventory(c)
	if err != nil {
		return err
	}
	byID := map[string]Unit{}
	for _, u := range units {
		byID[u.ID] = u
	}
	per := map[string][]DecHandoff{}
	noArea, total := 0, 0
	for _, key := range cl.DecAgents {
		rows, bad, err := readJSONL[Row](filepath.Join(c.trace, "fragments", key+".jsonl"))
		if err != nil {
			return fmt.Errorf("decision fragment %s: %w", key, err)
		}
		if len(bad) > 0 {
			return fmt.Errorf("decision fragment has unparseable rows: %s", bad[0])
		}
		for _, r := range rows {
			if unitType(r.Unit) != "DEC" {
				continue
			}
			total++
			if r.Area == "" {
				noArea++
				continue
			}
			owner, err := cl.ownerByPath(r.Area)
			if err != nil {
				return err
			}
			a := cl.agent(owner)
			if a == nil || a.Wave <= 1 {
				noArea++
				continue
			}
			u := byID[r.Unit]
			per[owner] = append(per[owner], DecHandoff{Unit: r.Unit, Title: u.Title, Note: u.Path, Summary: r.Summary,
				FirstVerdict: r.Disposition, FirstReason: r.Reason, ClassifiedBy: key})
		}
	}
	for _, key := range sortedKeys(per) {
		if err := writeJSONL(filepath.Join(c.trace, "assignments", key+".dec.jsonl"), per[key]); err != nil {
			return err
		}
		fmt.Println(key, len(per[key]), "decisions")
	}
	fmt.Println(total, "decisions classified,", total-noArea, "handed to an area or library agent,", noArea, "stay with the decision agents")
	return nil
}
