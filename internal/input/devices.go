package input

import "strings"

type Device struct {
	Name      string
	IsDefault bool
}

// The FFI lists devices newline-separated, the default with a leading '*'.
func parseDevices(listed string) []Device {
	if strings.TrimSpace(listed) == "" {
		return nil
	}

	lines := strings.Split(listed, "\n")
	devices := make([]Device, 0, len(lines))
	seen := make(map[string]int, len(lines))

	for _, line := range lines {
		if line == "" {
			continue
		}
		name, isDefault := strings.CutPrefix(line, "*")
		if index, exists := seen[name]; exists {
			// Keep the default marker if a repeated backend entry carries it.
			devices[index].IsDefault = devices[index].IsDefault || isDefault
			continue
		}
		seen[name] = len(devices)
		devices = append(devices, Device{Name: name, IsDefault: isDefault})
	}
	return devices
}
