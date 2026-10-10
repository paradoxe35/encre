//go:build !darwin

package main

func showInDock() {
}

func hideFromDock() {
}

// Windows and Linux move the keyboard on by themselves when a window goes.
func yieldFocus() {}
