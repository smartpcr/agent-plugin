package session

// SetAnchorLimit sets how many anchors sessions keep for their deliverer and
// returns the restore.
func SetAnchorLimit(n int) func() {
	old := anchorLimit
	anchorLimit = n
	return func() { anchorLimit = old }
}

// DeliveryState is how many anchors s holds for its deliverer (the appender's
// and the deliverer's own splits) and the appender's limit.
func DeliveryState(s Session) (anchors, refine, limit int) {
	l := s.(*live)
	l.dmu.Lock()
	defer l.dmu.Unlock()
	return len(l.anchors), len(l.refine), anchorLimit
}
