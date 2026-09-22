package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"sync"
	"time"
)

const (
	dayLayout = "2006-01-02"
	// A runaway loop must not fill the disk on a machine that never restarts.
	maxFileBytes = 10 << 20
	keepDays     = 30
	maxFiles     = 60
)

var logName = regexp.MustCompile(`^encre-(\d{4}-\d{2}-\d{2})(?:\.(\d+))?\.log$`)

// rotatingFile writes one file per day, rolls to a numbered file when the day's file
// reaches the cap, and prunes old files each time a new day starts. The file is opened
// on the first write, so a quiet day leaves nothing behind.
type rotatingFile struct {
	dir      string
	now      func() time.Time
	maxBytes int64
	keepDays int
	maxFiles int

	mu    sync.Mutex
	day   string
	index int
	size  int64
	file  *os.File
}

func newRotatingFile(dir string, now func() time.Time) *rotatingFile {
	return &rotatingFile{
		dir:      dir,
		now:      now,
		maxBytes: maxFileBytes,
		keepDays: keepDays,
		maxFiles: maxFiles,
	}
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	day := r.now().Format(dayLayout)
	if r.file == nil || day != r.day || r.size+int64(len(p)) > r.maxBytes {
		if err := r.open(day); err != nil {
			return 0, err
		}
	}

	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.close()
}

// open moves to the next file: the day's first on a new day, otherwise the next number.
// A file already at the cap, as after a restart, is skipped.
func (r *rotatingFile) open(day string) error {
	if err := r.close(); err != nil {
		return err
	}

	if day != r.day {
		r.day = day
		r.index = highestIndex(r.dir, day)
	} else {
		r.index++
	}
	prune(r.dir, r.now(), r.keepDays, r.maxFiles)

	for {
		file, err := os.OpenFile(r.path(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}
		info, err := file.Stat()
		if err != nil {
			file.Close()
			return fmt.Errorf("failed to stat log file: %w", err)
		}
		if info.Size() < r.maxBytes {
			r.file = file
			r.size = info.Size()
			return nil
		}
		file.Close()
		r.index++
	}
}

func (r *rotatingFile) close() error {
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	r.size = 0
	return err
}

func (r *rotatingFile) path() string {
	return filepath.Join(r.dir, fileName(r.day, r.index))
}

func fileName(day string, index int) string {
	if index == 0 {
		return fmt.Sprintf("encre-%s.log", day)
	}
	return fmt.Sprintf("encre-%s.%d.log", day, index)
}

type logFile struct {
	name  string
	day   string
	index int
}

// listLogs returns the log files in the directory, oldest first.
func listLogs(dir string) []logFile {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var logs []logFile
	for _, entry := range entries {
		match := logName.FindStringSubmatch(entry.Name())
		if entry.IsDir() || match == nil {
			continue
		}
		index, _ := strconv.Atoi(match[2])
		logs = append(logs, logFile{name: entry.Name(), day: match[1], index: index})
	}
	sort.Slice(logs, func(i, j int) bool {
		if logs[i].day != logs[j].day {
			return logs[i].day < logs[j].day
		}
		return logs[i].index < logs[j].index
	})
	return logs
}

func highestIndex(dir, day string) int {
	highest := 0
	for _, log := range listLogs(dir) {
		if log.day == day && log.index > highest {
			highest = log.index
		}
	}
	return highest
}

// prune removes files older than the retention window, then the oldest until the file
// about to be opened fits under the count cap. It never logs: it runs under the writer's lock.
func prune(dir string, now time.Time, keepDays, maxFiles int) {
	logs := listLogs(dir)
	oldest := now.AddDate(0, 0, -keepDays).Format(dayLayout)

	var kept []logFile
	for _, log := range logs {
		if log.day < oldest {
			os.Remove(filepath.Join(dir, log.name))
			continue
		}
		kept = append(kept, log)
	}

	for len(kept) >= maxFiles {
		os.Remove(filepath.Join(dir, kept[0].name))
		kept = kept[1:]
	}
}
