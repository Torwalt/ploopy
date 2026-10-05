//go:build !linux && !darwin

package ui

func flushInput(int) error { return nil }
