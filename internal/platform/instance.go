package platform

import (
	"bufio"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	singleinstance "github.com/allan-simon/go-singleinstance"
	"github.com/paradoxe35/encre/internal/logger"
)

// Loopback only, which also keeps macOS from asking about incoming connections.
type Handover struct {
	portPath string
	listener net.Listener
}

const (
	showRequest    = "show"
	connectTimeout = 500 * time.Millisecond
	notifyAttempts = 10
	notifyPause    = 200 * time.Millisecond
)

func NewHandover(portPath string) *Handover {
	return &Handover{portPath: portPath}
}

// A failure here only loses the handover, so it is reported rather than fatal.
func (h *Handover) Serve(onShow func()) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		logger.Error("No local listener, so a second launch cannot reach this one", "error", err)
		// A port left from an earlier run would send the next launch to another process.
		os.Remove(h.portPath)
		return
	}
	h.listener = listener

	port := listener.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(h.portPath, []byte(strconv.Itoa(port)), 0o600); err != nil {
		logger.Error("Could not record the handover port", "error", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			// A client that never finishes its line must not block every later launch.
			conn.SetReadDeadline(time.Now().Add(connectTimeout))
			request, _ := bufio.NewReader(conn).ReadString('\n')
			conn.Close()
			if strings.TrimSpace(request) == showRequest {
				onShow()
			}
		}
	}()
}

func (h *Handover) Close() {
	if h.listener != nil {
		h.listener.Close()
		h.listener = nil
	}
	os.Remove(h.portPath)
}

// Instance is this copy's claim to be the Encre running: the lock, and the handover later launches reach.
type Instance struct {
	lockPath string

	mu       sync.Mutex
	lock     *os.File
	handover *Handover
	onShow   func()
	// handOver and resume replace Release and Reclaim around a restart.
	handOver func()
	resume   func()
}

var claimed atomic.Pointer[Instance]

func Claim(lockPath, portPath string) (*Instance, error) {
	lock, err := singleinstance.CreateLockFile(lockPath)
	if err != nil {
		return nil, err
	}
	// A crashed run's port may belong to a stranger by now; until Serve, launches should find none.
	os.Remove(portPath)
	i := &Instance{lockPath: lockPath, lock: lock, handover: NewHandover(portPath)}
	claimed.Store(i)
	return i, nil
}

func (i *Instance) SetHandover(handOver, resume func()) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.handOver, i.resume = handOver, resume
}

func (i *Instance) Serve(onShow func()) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.onShow = onShow
	i.handover.Serve(onShow)
}

// Release lets a copy started in our place claim the lock and handover for itself.
func (i *Instance) Release() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.lock == nil {
		return
	}
	i.handover.Close()
	i.lock.Close()
	i.lock = nil
}

// Reclaim takes back what Release let go, for when the new copy never started.
func (i *Instance) Reclaim() error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.lock != nil {
		return nil
	}
	lock, err := singleinstance.CreateLockFile(i.lockPath)
	if err != nil {
		return err
	}
	i.lock = lock
	if i.onShow != nil {
		i.handover.Serve(i.onShow)
	}
	return nil
}

// Close is a no-op once released, so an exiting copy leaves its successor's handover alone.
func (i *Instance) Close() {
	i.Release()
}

// startInOurPlace runs start with the claim let go, and takes it back if start fails.
func (i *Instance) startInOurPlace(start func() error) error {
	if i == nil {
		return start()
	}
	i.mu.Lock()
	handOver, resume := i.handOver, i.resume
	i.mu.Unlock()
	if handOver == nil {
		handOver = i.Release
	}
	if resume == nil {
		resume = func() {
			if err := i.Reclaim(); err != nil {
				logger.Error("Could not take back the instance lock", "error", err)
			}
		}
	}

	handOver()
	err := start()
	if err != nil {
		resume()
	}
	return err
}

// Retried: the copy holding the lock may still be starting and not have written its port yet.
func Notify(portPath string) bool {
	for attempt := 0; attempt < notifyAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(notifyPause)
		}

		raw, err := os.ReadFile(portPath)
		if err != nil {
			continue
		}
		port, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil || port < 1 || port > 65535 {
			continue
		}

		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), connectTimeout)
		if err != nil {
			continue
		}
		_, err = conn.Write([]byte(showRequest + "\n"))
		conn.Close()
		if err == nil {
			return true
		}
	}

	return false
}
