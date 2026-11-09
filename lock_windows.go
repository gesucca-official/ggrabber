package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// tryLock prende un lock esclusivo sul file; il sistema lo rilascia da solo
// anche se il processo termina male, quindi non restano lock "orfani".
// Restituisce false solo se il lock è davvero tenuto da un altro processo: se il
// filesystem non supporta i lock, si procede.
func tryLock(f *os.File) bool {
	ol := new(windows.Overlapped)
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, ol)
	return !errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
