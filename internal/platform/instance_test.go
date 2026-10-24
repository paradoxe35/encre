package platform

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHandoverDeliversShowRequest(t *testing.T) {
	portPath := filepath.Join(t.TempDir(), "instance.port")
	shown := make(chan struct{}, 1)

	handover := NewHandover(portPath)
	handover.Serve(func() { shown <- struct{}{} })
	defer handover.Close()

	if !Notify(portPath) {
		t.Fatal("the running copy did not take the request")
	}

	select {
	case <-shown:
	case <-time.After(2 * time.Second):
		t.Fatal("show was never delivered")
	}
}

func TestNotifyWithoutARunningCopy(t *testing.T) {
	if Notify(filepath.Join(t.TempDir(), "absent.port")) {
		t.Fatal("reported a handover with nothing listening")
	}
}

func TestCloseRemovesThePortFile(t *testing.T) {
	portPath := filepath.Join(t.TempDir(), "instance.port")

	handover := NewHandover(portPath)
	handover.Serve(func() {})
	handover.Close()

	// A port left behind would send the next launch at whatever takes that port next.
	if Notify(portPath) {
		t.Fatal("the port file outlived the listener")
	}
}

// A client that connects and says nothing must not hold up the next launch's request.
func TestASilentClientDoesNotWedgeTheHandover(t *testing.T) {
	portPath := filepath.Join(t.TempDir(), "instance.port")
	shown := make(chan struct{}, 1)

	handover := NewHandover(portPath)
	handover.Serve(func() { shown <- struct{}{} })
	defer handover.Close()

	port, err := os.ReadFile(portPath)
	if err != nil {
		t.Fatal(err)
	}
	silent, err := net.Dial("tcp", "127.0.0.1:"+strings.TrimSpace(string(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer silent.Close()

	if !Notify(portPath) {
		t.Fatal("the running copy did not take the request")
	}
	select {
	case <-shown:
	case <-time.After(3 * time.Second):
		t.Fatal("show was never delivered behind a silent client")
	}
}

func instancePaths(t *testing.T) (lockPath, portPath string) {
	dir := t.TempDir()
	return filepath.Join(dir, "encre.lock"), filepath.Join(dir, "instance.port")
}

func TestASecondClaimIsRefused(t *testing.T) {
	lockPath, portPath := instancePaths(t)
	first, err := Claim(lockPath, portPath)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()

	if second, err := Claim(lockPath, portPath); err == nil {
		second.Close()
		t.Fatal("two copies held the lock")
	}
}

// A copy started in our place must claim the lock itself, not hand its launch back to us.
func TestAReleasedClaimPassesToTheNextCopy(t *testing.T) {
	lockPath, portPath := instancePaths(t)
	old, err := Claim(lockPath, portPath)
	if err != nil {
		t.Fatal(err)
	}
	oldShown := make(chan struct{}, 1)
	old.Serve(func() { oldShown <- struct{}{} })
	old.Release()

	next, err := Claim(lockPath, portPath)
	if err != nil {
		t.Fatalf("the next copy could not claim: %v", err)
	}
	defer next.Close()
	nextShown := make(chan struct{}, 1)
	next.Serve(func() { nextShown <- struct{}{} })

	old.Close()
	if !Notify(portPath) {
		t.Fatal("the old copy's exit took the next copy's handover with it")
	}
	select {
	case <-nextShown:
	case <-oldShown:
		t.Fatal("the launch went to the old copy")
	case <-time.After(2 * time.Second):
		t.Fatal("show was never delivered")
	}
}

func TestReclaimAfterAFailedHandover(t *testing.T) {
	lockPath, portPath := instancePaths(t)
	inst, err := Claim(lockPath, portPath)
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close()
	shown := make(chan struct{}, 1)
	inst.Serve(func() { shown <- struct{}{} })

	inst.Release()
	if err := inst.Reclaim(); err != nil {
		t.Fatal(err)
	}

	if other, err := Claim(lockPath, portPath); err == nil {
		other.Close()
		t.Fatal("the reclaimed lock was free")
	}
	if !Notify(portPath) {
		t.Fatal("the reclaimed copy took no launches")
	}
	select {
	case <-shown:
	case <-time.After(2 * time.Second):
		t.Fatal("show was never delivered")
	}
}

// A crashed run leaves its port behind, and whatever listens there now is not Encre.
func TestClaimForgetsACrashedRunsPort(t *testing.T) {
	lockPath, portPath := instancePaths(t)
	stranger, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer stranger.Close()
	port := strconv.Itoa(stranger.Addr().(*net.TCPAddr).Port)
	if err := os.WriteFile(portPath, []byte(port), 0o600); err != nil {
		t.Fatal(err)
	}

	inst, err := Claim(lockPath, portPath)
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close()

	if _, err := os.Stat(portPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the stale port file survived the claim: %v", err)
	}
}

// A restart must stop the shortcuts before the new copy starts, and bring them back if it never does.
func TestStartInOurPlaceRunsTheHandover(t *testing.T) {
	lockPath, portPath := instancePaths(t)
	inst, err := Claim(lockPath, portPath)
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close()

	var steps []string
	inst.SetHandover(func() {
		steps = append(steps, "hand over")
		inst.Release()
	}, func() {
		steps = append(steps, "resume")
		inst.Reclaim()
	})

	inst.startInOurPlace(func() error {
		steps = append(steps, "start")
		return errors.New("no such file")
	})
	if got := strings.Join(steps, ", "); got != "hand over, start, resume" {
		t.Fatalf("ran %s", got)
	}

	steps = nil
	inst.startInOurPlace(func() error {
		steps = append(steps, "start")
		return nil
	})
	if got := strings.Join(steps, ", "); got != "hand over, start" {
		t.Fatalf("ran %s", got)
	}
}

func TestStartInOurPlaceReclaimsWhenTheStartFails(t *testing.T) {
	lockPath, portPath := instancePaths(t)
	inst, err := Claim(lockPath, portPath)
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close()
	inst.Serve(func() {})

	var freeDuringStart bool
	err = inst.startInOurPlace(func() error {
		if other, err := Claim(lockPath, portPath); err == nil {
			freeDuringStart = true
			other.Close()
		}
		return errors.New("no such file")
	})
	if err == nil {
		t.Fatal("the failed start was not reported")
	}
	if !freeDuringStart {
		t.Fatal("the new copy would have found the lock still held")
	}
	if other, err := Claim(lockPath, portPath); err == nil {
		other.Close()
		t.Fatal("the lock was not taken back after the failed start")
	}
}
