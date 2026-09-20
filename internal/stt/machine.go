package stt

import (
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// What can cheaply be learned about the computer, to rank models by whether they will keep up.
type Machine struct {
	Cores    int
	MemoryMB int
}

// referenceCores matches the slowest machine the catalog's realtime factors
// were measured on, so scaling from it errs toward caution.
const referenceCores = 8

var (
	machineOnce sync.Once
	machine     Machine
)

func Host() Machine {
	machineOnce.Do(func() {
		machine = Machine{Cores: runtime.NumCPU(), MemoryMB: totalMemoryMB()}
	})
	return machine
}

// EstimatedRealtime scales the catalog's measured factor by core count. A heuristic, not a
// benchmark: cores are a rough proxy for throughput and say nothing about clock speed or vector width.
func (m Model) EstimatedRealtime(host Machine) float64 {
	if m.RealtimeFactor <= 0 || host.Cores <= 0 {
		return 0
	}
	return m.RealtimeFactor * float64(host.Cores) / referenceCores
}

// FitsMemory keeps a model well under total RAM; one that barely fits pushes the machine into swap.
func (m Model) FitsMemory(host Machine) bool {
	if host.MemoryMB <= 0 {
		return true
	}
	return m.SizeMB() < float64(host.MemoryMB)*0.4
}

// Comfortable means the model transcribes faster than you can speak, with headroom, and fits in memory.
func (m Model) Comfortable(host Machine) bool {
	return m.FitsMemory(host) && m.EstimatedRealtime(host) >= 2
}

type Fit int

const (
	FitComfortable Fit = iota
	// No measured realtime factor (e.g. a user-dropped model file); ranks below known-good
	// but "slow" would be a claim the catalog can't support.
	FitUnknown
	FitSlow
	FitTooLarge
)

func (m Model) Fit(host Machine) Fit {
	switch {
	case !m.FitsMemory(host):
		return FitTooLarge
	case m.RealtimeFactor <= 0:
		return FitUnknown
	case m.EstimatedRealtime(host) < 2:
		return FitSlow
	default:
		return FitComfortable
	}
}

func (f Fit) Label() string {
	switch f {
	case FitTooLarge:
		return "may not fit in memory"
	case FitSlow:
		return "slow on this machine"
	case FitUnknown:
		return "speed unknown"
	default:
		return ""
	}
}

func totalMemoryMB() int {
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if !strings.HasPrefix(line, "MemTotal:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				if kb, err := strconv.Atoi(fields[1]); err == nil {
					return kb / 1024
				}
			}
		}
	}
	return sysMemoryMB()
}

func RankForMachine(models []Model, host Machine, downloaded func(Model) bool) {
	sort.SliceStable(models, func(i, j int) bool {
		a, b := models[i], models[j]

		if downloaded != nil {
			if has, other := downloaded(a), downloaded(b); has != other {
				return has
			}
		}
		if fa, fb := a.Fit(host), b.Fit(host); fa != fb {
			return fa < fb
		}
		if a.Recommended != b.Recommended {
			return a.Recommended
		}
		return a.AccuracyScore > b.AccuracyScore
	})
}
