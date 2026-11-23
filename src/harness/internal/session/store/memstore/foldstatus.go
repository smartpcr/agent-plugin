package memstore

import (
	"context"
	"slices"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

var _ store.FoldRows = (*Store)(nil)

// FoldCounts implements store.FoldRows against what another process would see:
// each session's durable records and durable derived rows.
func (s *Store) FoldCounts(ctx context.Context, want map[string]int) ([]store.FoldCount, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(want))
	for k := range want {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.FoldCount, 0, len(keys))
	for _, k := range keys {
		c := store.FoldCount{Key: k, StateVersion: want[k]}
		for _, ss := range s.sessions {
			if ss.deleted || ss.durable == 0 {
				continue
			}
			row, ok := ss.durableCk[ckKey{k, want[k]}]
			switch {
			case !ok:
				c.Pending++
			case row.Seq+1 >= store.Seq(ss.durable):
				c.Current++
			default:
				c.Behind++
			}
		}
		out = append(out, c)
	}
	return out, nil
}

// FoldPending implements store.FoldRows.
func (s *Store) FoldPending(ctx context.Context, key string, version int, after string, limit int) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	var out []string
	for id, ss := range s.sessions {
		if ss.deleted || ss.durable == 0 || id <= after {
			continue
		}
		if _, ok := ss.durableCk[ckKey{key, version}]; !ok {
			out = append(out, id)
		}
	}
	s.mu.Unlock()
	slices.Sort(out)
	if limit <= 0 {
		return nil, nil
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
