package cordis

// FiberState is the lifecycle state of a fiber.
type FiberState int

const (
	// Pending: waiting for a declared service.
	Pending FiberState = iota
	// Loading: the component body, Init, or an effect started by them is running.
	Loading
	// Active: loaded; the fiber's services are visible to dependents.
	Active
	// Failed: the last load failed. The fiber stays failed until Update or a
	// registry Replace clears the error; provider changes alone do not re-run it.
	Failed
	// Disposed: removed from its parent; terminal.
	Disposed
	// Unloading: waiting for dependents, then releasing effects.
	Unloading
)

func (s FiberState) String() string {
	switch s {
	case Pending:
		return "Pending"
	case Loading:
		return "Loading"
	case Active:
		return "Active"
	case Failed:
		return "Failed"
	case Disposed:
		return "Disposed"
	case Unloading:
		return "Unloading"
	default:
		return "FiberState(?)"
	}
}
