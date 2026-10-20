//go:build !unix

package config

import "os"

func ownedByUser(os.FileInfo) bool { return true }
