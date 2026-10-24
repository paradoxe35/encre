package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/utils"
)

type Kind string

const (
	KindRevise    Kind = "revise"
	KindTranslate Kind = "translate"
	KindDictate   Kind = "dictate"
	KindAsk       Kind = "ask"
)

const MaxEntries = 200

// Language fields are only meaningful for translations.
type Entry struct {
	ID         string    `json:"id"`
	Kind       Kind      `json:"kind"`
	At         time.Time `json:"at"`
	Original   string    `json:"original"`
	Result     string    `json:"result"`
	FromLang   string    `json:"from_lang,omitempty"`
	ToLang     string    `json:"to_lang,omitempty"`
	Model      string    `json:"model,omitempty"`
	Provider   string    `json:"provider,omitempty"`
	Characters int       `json:"characters"`
	// Follows is the exchange this one continues, so an Ask conversation can branch.
	Follows string `json:"follows,omitempty"`
}

func NewID() string { return time.Now().Format("20060102150405.000000000") }

// Reads load the whole file, which trimming keeps under 2*MaxEntries lines.
type Store struct {
	mu   sync.Mutex
	path string

	// lines is counted on the first Add, then kept up to date, so trimming needs no reread.
	lines   int
	counted bool

	onChange func()
}

// maxLine bounds a single entry; a longer line is skipped rather than read into memory.
const maxLine = 1 << 20

// fileMu spans stores: the app opens more than one on the same file, and a trim must not drop another's append.
var fileMu sync.Mutex

func NewStore() *Store { return &Store{path: utils.AppHomeDir("history.jsonl")} }

// Fired outside the lock, so a handler may read the store back without deadlocking.
func (s *Store) OnChange(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.onChange = fn
}

// A failed write is logged and dropped: history must never break the action it records.
func (s *Store) Add(entry Entry) {
	var notify func()
	defer func() {
		if notify != nil {
			notify()
		}
	}()

	s.mu.Lock()
	defer s.mu.Unlock()
	fileMu.Lock()
	defer fileMu.Unlock()

	if entry.At.IsZero() {
		entry.At = time.Now()
	}
	if entry.ID == "" {
		entry.ID = NewID()
	}

	if !s.counted {
		s.lines = countLines(s.path)
		s.counted = true
		// Older versions wrote it world-readable.
		if err := utils.MakePrivate(s.path); err != nil {
			logger.Warn("Failed to restrict history file permissions", "path", s.path, "error", err)
		}
	}

	file, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		logger.Error("Failed to open history file", "path", s.path, "error", err)
		return
	}
	err = json.NewEncoder(file).Encode(entry)
	// Closed before trimming: Windows cannot rename over an open file.
	file.Close()
	if err != nil {
		logger.Error("Failed to write history entry", "path", s.path, "error", err)
		return
	}

	s.lines++
	if s.lines > 2*MaxEntries {
		s.trimLocked()
	}
	notify = s.onChange
}

func countLines(path string) int {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()

	count := 0
	buf := make([]byte, 32*1024)
	for {
		n, err := file.Read(buf)
		count += bytes.Count(buf[:n], []byte{'\n'})
		if err != nil {
			return count
		}
	}
}

func (s *Store) Recent(kind Kind) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	fileMu.Lock()
	defer fileMu.Unlock()

	file, err := os.Open(s.path)
	if err != nil {
		return nil
	}
	defer file.Close()

	all := readEntries(file)
	if len(all) > MaxEntries {
		all = all[len(all)-MaxEntries:]
	}

	var entries []Entry
	for i := len(all) - 1; i >= 0; i-- {
		if kind == "" || all[i].Kind == kind {
			entries = append(entries, all[i])
		}
	}
	return entries
}

// readEntries skips lines that are too long or will not parse, so one bad line costs only itself.
func readEntries(r io.Reader) []Entry {
	var entries []Entry
	reader := bufio.NewReaderSize(r, 64*1024)
	var line []byte
	tooLong := false
	for {
		chunk, err := reader.ReadSlice('\n')
		if !tooLong {
			line = append(line, chunk...)
			if len(line) > maxLine {
				tooLong = true
				line = line[:0]
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}

		var entry Entry
		if !tooLong && len(bytes.TrimSpace(line)) > 0 && json.Unmarshal(line, &entry) == nil {
			entries = append(entries, entry)
		}
		line = line[:0]
		tooLong = false

		if err != nil {
			return entries
		}
	}
}

func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fileMu.Lock()
	defer fileMu.Unlock()
	// Removing a symlink would leave every entry in the file it points to; emptying goes through it.
	if err := os.Truncate(s.path, 0); err != nil && !os.IsNotExist(err) {
		logger.Error("Failed to clear history file", "path", s.path, "error", err)
		return err
	}
	s.lines = 0
	s.counted = true
	return nil
}

// Cutting back to MaxEntries once past twice that amortises to one rewrite every MaxEntries appends.
func (s *Store) trimLocked() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		logger.Error("Failed to read history file for trimming", "path", s.path, "error", err)
		return
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	s.lines = len(lines)
	if len(lines) <= MaxEntries {
		return
	}
	keep := strings.Join(lines[len(lines)-MaxEntries:], "\n") + "\n"
	if err := utils.WriteFileAtomic(s.path, []byte(keep), 0o600); err != nil {
		logger.Error("Failed to trim history file", "path", s.path, "error", err)
		return
	}
	s.lines = MaxEntries
}
