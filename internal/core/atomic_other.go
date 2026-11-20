//go:build !windows

package core

import "os"

func renameAtomic(oldPath, newPath string) error { return os.Rename(oldPath, newPath) }
