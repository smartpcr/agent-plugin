package sqlite

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"time"
)

// The prune of derived rows (session-views.md, "a prune removes rows no binary
// registers"; M1-G-STORE-16, DH-T-VIEW-8). The checkpoints table holds the
// core's rows (keys under "core/") and the views' rows. A view row whose key no
// view of this binary registers, or whose state version is lower than the one it
// registers, is dead weight: no reader of this binary uses it. It is safe to
// delete only when no live instance on the home runs another build, which may
// still read it; so the prune runs only when every live hosts row carries this
// binary's binary_version. The core's rows are never touched (Rebuild owns them).

// DutyPruneDerived is the duty that prunes derived rows, once per home.
const DutyPruneDerived = "views-prune"

// PruneReport is what PruneDerived did.
type PruneReport struct {
	// Deleted is the number of checkpoints rows removed.
	Deleted int64
	// Blocked names each live instance of another build (pid, kind, start and
	// its binary_version). When it is set nothing was deleted.
	Blocked []string
}

// PruneBlockers is the guard of the prune: it names each live instance on the
// home whose binary_version differs from this binary's. The prune may run only
// when it returns none. It changes nothing.
func (s *Store) PruneBlockers(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []string
	err := s.read(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = s.pruneBlockers(ctx, tx)
		return err
	})
	return out, err
}

func (s *Store) pruneBlockers(ctx context.Context, q querier) ([]string, error) {
	rows, err := s.hostRows(ctx, q, true)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, h := range rows {
		if h.version == s.version {
			continue
		}
		st, err := s.probeLock(h.instance)
		if err != nil {
			return nil, err
		}
		if st != lockFree {
			out = append(out, h.String()+" runs build "+quoteVersion(h.version))
		}
	}
	return out, nil
}

func quoteVersion(v string) string {
	if v == "" {
		return `"" (unrecorded)`
	}
	return `"` + v + `"`
}

// PruneDerived deletes, in one write transaction, every checkpoints row whose key
// is not under "core/" and either is not in known or has a state version lower
// than known[key]; known maps each view key this binary registers to its state
// version. The guard (PruneBlockers) is checked again inside the transaction:
// when a live instance of another build is found, nothing is deleted and the
// report names it.
func (s *Store) PruneDerived(ctx context.Context, known map[string]int) (PruneReport, error) {
	if err := ctx.Err(); err != nil {
		return PruneReport{}, err
	}
	if err := s.admit(); err != nil {
		return PruneReport{}, err
	}
	var rep PruneReport
	err := s.write(ctx, func(tx *sql.Tx) error {
		rep = PruneReport{}
		blocked, err := s.pruneBlockers(ctx, tx)
		if err != nil || len(blocked) > 0 {
			rep.Blocked = blocked
			return err
		}
		keys := make([]any, 0, 2*len(known)+1)
		keys = append(keys, coreKeyPrefix)
		clauses := make([]string, 0, len(known))
		for k, v := range known {
			clauses = append(clauses, `(key = ? AND state_version >= ?)`)
			keys = append(keys, k, v)
		}
		keep := `0`
		if len(clauses) > 0 {
			keep = strings.Join(clauses, ` OR `)
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM checkpoints WHERE substr(key, 1, `+strconv.Itoa(len(coreKeyPrefix))+`) <> ?
			AND NOT (`+keep+`)`, keys...)
		if err != nil {
			return err
		}
		rep.Deleted, err = res.RowsAffected()
		return err
	})
	return rep, err
}

// RunPrune runs PruneDerived as the prune duty: on one instance of the home, at
// most once per every, and only while the guard finds no live instance of
// another build (otherwise it returns at once, not ran, with the blockers).
func (s *Store) RunPrune(ctx context.Context, known map[string]int, every time.Duration) (bool, PruneReport, error) {
	blocked, err := s.PruneBlockers(ctx)
	if err != nil || len(blocked) > 0 {
		return false, PruneReport{Blocked: blocked}, err
	}
	var rep PruneReport
	ran, err := s.RunDuty(ctx, DutyPruneDerived, every, func(ctx context.Context) error {
		var err error
		rep, err = s.PruneDerived(ctx, known)
		return err
	})
	return ran, rep, err
}
