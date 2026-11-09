//go:build !windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// tryLock prende un lock esclusivo sul file; il sistema lo rilascia da solo
// anche se il processo termina male, quindi non restano lock "orfani".
// Restituisce false solo se il lock è davvero tenuto da un altro processo: se il
// filesystem (es. alcune condivisioni di rete) non supporta i lock, si procede.
func tryLock(f *os.File) bool {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	return !errors.Is(err, unix.EWOULDBLOCK)
}
