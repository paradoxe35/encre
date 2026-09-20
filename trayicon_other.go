//go:build !linux

package main

import "fyne.io/fyne/v2"

func trayIcon() fyne.Resource {
	return resourceIconPng
}
