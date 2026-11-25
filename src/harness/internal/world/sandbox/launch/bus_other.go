//go:build !linux

package launch

// sessionBusUnreachable is never shown off Linux: no backend there can shut a
// same-user service out, so delegated-execution stays missing (I2 Q4).
func sessionBusUnreachable() bool { return false }
