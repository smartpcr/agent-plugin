package sqlite

import "context"

// Synchronous is PRAGMA synchronous on the write connection, the one every
// commit runs on (2: FULL).
func (s *Store) Synchronous(ctx context.Context) (int64, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	var v int64
	err := s.wconn.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&v)
	return v, err
}

// TornDown reports whether the write connection and pool are closed.
func (s *Store) TornDown() bool {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.wconn == nil && s.wdb == nil
}
