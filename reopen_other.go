//go:build !darwin

package main

func installReopenHandler(show func()) {}

func installTerminateHandler(stop func(asked bool) bool) {}
