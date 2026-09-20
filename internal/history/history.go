package history

import (
	"bufio"
	"encoding/json"
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
	KindSpeech    Kind = "speech"
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
}

// Append-only JSONL log capped at MaxEntries; reads load the whole file, which stays
// small by construction.
type Store struct {
	mu   sync.Mutex
	path string

	onChange func()
}

func NewStore() *Store { return &Store{path: utils.AppHomeDir("history.jsonl")} }

// Fired after every successful append, on the writing goroutine and outside the lock, so a
// handler may read the store back without deadlocking.
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

	if entry.At.IsZero() {
		entry.At = time.Now()
	}
	if entry.ID == "" {
		entry.ID = entry.At.Format("20060102150405.000000000")
	}

	file, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		logger.Error("Failed to open history file", "path", s.path, "error", err)
		return
	}
	defer file.Close()

	if err := json.NewEncoder(file).Encode(entry); err != nil {
		logger.Error("Failed to write history entry", "path", s.path, "error", err)
		return
	}
	s.trimLocked()
	notify = s.onChange
}

func (s *Store) Recent(kind Kind) []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, err := os.Open(s.path)
	if err != nil {
		return nil
	}
	defer file.Close()

	var entries []Entry
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var entry Entry
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		if kind == "" || entry.Kind == kind {
			entries = append(entries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil
	}

	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries
}

func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		logger.Error("Failed to clear history file", "path", s.path, "error", err)
		return err
	}
	return nil
}

// The rewrite amortises to one every MaxEntries appends.
func (s *Store) trimLocked() {
	info, err := os.Stat(s.path)
	if err != nil || info.Size() < 64*MaxEntries {
		return
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		logger.Error("Failed to read history file for trimming", "path", s.path, "error", err)
		return
	}

	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) <= MaxEntries {
		return
	}
	keep := strings.Join(lines[len(lines)-MaxEntries:], "\n") + "\n"
	if err := os.WriteFile(s.path, []byte(keep), 0o644); err != nil {
		logger.Error("Failed to trim history file", "path", s.path, "error", err)
	}
}
