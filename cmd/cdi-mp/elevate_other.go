//go:build !windows

package main

func relaunchElevated(error) bool {
	return false
}
