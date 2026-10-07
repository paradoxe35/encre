//go:build !darwin

package main

func showInDock() {
}

func hideFromDock() {
}

// The window system hands focus back to the previous window by itself.
func rememberFrontmostApp() {}

func restoreFrontmostApp() {}
