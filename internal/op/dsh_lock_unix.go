//go:build !windows

package op

import (
	"errors"
	"syscall"
)

func dshLockHolderExited(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func dshRetryableReplace(err error) bool { return false }
