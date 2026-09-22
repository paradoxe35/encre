package platform

import (
	"bufio"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/paradoxe35/encre/internal/logger"
)

// Handover lets a launch that lost the instance lock reach the running copy; without it,
// clicking the launcher while Encre sits in the tray does nothing. Loopback only, which also
// keeps macOS from asking about incoming connections.
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
	}
	os.Remove(h.portPath)
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
