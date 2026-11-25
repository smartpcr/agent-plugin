//go:build windows

package launch

import (
	"os"

	"golang.org/x/sys/windows"
)

// closeOnExec clears the inherit flag of the channel ends. The command is started
// with an explicit handle list (subprocess.Become), so this is a second guard:
// no later child of the stage can inherit them either.
func closeOnExec(files ...*os.File) error {
	for _, f := range files {
		if err := windows.SetHandleInformation(windows.Handle(f.Fd()), windows.HANDLE_FLAG_INHERIT, 0); err != nil {
			return err
		}
	}
	return nil
}

// readOnly has no separate meaning on Windows: a refused open is a permission
// error.
func readOnly(error) bool { return false }

// homeReadOnly cannot be shown on Windows, which has no outside confiner in M2.
func homeReadOnly(string) bool { return false }
