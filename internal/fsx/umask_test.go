package fsx

import "golang.org/x/sys/unix"

// setUmask sets the process umask and returns the previous value. Used by
// the MkdirAll test to prove #C24 — that mkdir's mode argument is masked and
// an explicit chmod is required to deliver dir_mode.
//
// This is process-global, so tests using it must not run in parallel.
func setUmask(mask int) int { return unix.Umask(mask) }
