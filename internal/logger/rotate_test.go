package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) read() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestFile(t *testing.T) (*rotatingFile, *clock, string) {
	t.Helper()
	dir := t.TempDir()
	c := &clock{now: time.Date(2026, 9, 20, 23, 59, 0, 0, time.Local)}
	r := newRotatingFile(dir, c.read)
	t.Cleanup(func() { r.Close() })
	return r, c, dir
}

func write(t *testing.T, r *rotatingFile, line string) {
	t.Helper()
	if _, err := r.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func content(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func touch(t *testing.T, dir, name string, size int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Repeat("x", size)), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestNothingIsCreatedBeforeTheFirstWrite(t *testing.T) {
	r, _, dir := newTestFile(t)
	if got := names(t, dir); len(got) != 0 {
		t.Fatalf("files %v before any write", got)
	}

	write(t, r, "hello")
	if got := names(t, dir); !slices.Equal(got, []string{"encre-2026-09-20.log"}) {
		t.Fatalf("files %v", got)
	}
}

func TestADayChangeMovesToTheNewDaysFile(t *testing.T) {
	r, c, dir := newTestFile(t)
	write(t, r, "before midnight")

	c.advance(2 * time.Minute)
	write(t, r, "after midnight")

	if got := content(t, dir, "encre-2026-09-20.log"); got != "before midnight\n" {
		t.Fatalf("old day holds %q", got)
	}
	if got := content(t, dir, "encre-2026-09-21.log"); got != "after midnight\n" {
		t.Fatalf("new day holds %q", got)
	}
}

func TestAQuietDayLeavesNoFile(t *testing.T) {
	r, c, dir := newTestFile(t)
	write(t, r, "day one")

	c.advance(48 * time.Hour)
	write(t, r, "day three")

	got := names(t, dir)
	if !slices.Equal(got, []string{"encre-2026-09-20.log", "encre-2026-09-22.log"}) {
		t.Fatalf("files %v", got)
	}
}

func TestAFullFileRollsToANumberedOne(t *testing.T) {
	r, _, dir := newTestFile(t)
	r.maxBytes = 32
	write(t, r, strings.Repeat("x", 25))
	write(t, r, "spills over")
	write(t, r, "same file")

	got := names(t, dir)
	if !slices.Equal(got, []string{"encre-2026-09-20.1.log", "encre-2026-09-20.log"}) {
		t.Fatalf("files %v", got)
	}
	if got := content(t, dir, "encre-2026-09-20.1.log"); got != "spills over\nsame file\n" {
		t.Fatalf("rolled file holds %q", got)
	}
}

func TestARestartAppendsToTheDaysLatestFile(t *testing.T) {
	r, _, dir := newTestFile(t)
	r.maxBytes = 32
	touch(t, dir, "encre-2026-09-20.log", 32)
	touch(t, dir, "encre-2026-09-20.1.log", 5)
	touch(t, dir, "encre-2026-09-20.2.log", 5)

	write(t, r, "back")
	if got := content(t, dir, "encre-2026-09-20.2.log"); got != "xxxxxback\n" {
		t.Fatalf("latest file holds %q", got)
	}
}

func TestARestartSkipsADaysFileAlreadyAtTheCap(t *testing.T) {
	r, _, dir := newTestFile(t)
	r.maxBytes = 32
	touch(t, dir, "encre-2026-09-20.log", 32)

	write(t, r, "back")
	if got := content(t, dir, "encre-2026-09-20.1.log"); got != "back\n" {
		t.Fatalf("next file holds %q", got)
	}
}

func TestNumberedFilesSortNumerically(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"encre-2026-09-20.10.log", "encre-2026-09-20.2.log", "encre-2026-09-20.log", "encre-2026-09-19.log"} {
		touch(t, dir, n, 1)
	}

	var got []string
	for _, log := range listLogs(dir) {
		got = append(got, log.name)
	}
	want := []string{"encre-2026-09-19.log", "encre-2026-09-20.log", "encre-2026-09-20.2.log", "encre-2026-09-20.10.log"}
	if !slices.Equal(got, want) {
		t.Fatalf("order %v", got)
	}
	if got := highestIndex(dir, "2026-09-20"); got != 10 {
		t.Fatalf("highest index %d", got)
	}
}

func TestOldFilesArePrunedWhenADayStarts(t *testing.T) {
	r, c, dir := newTestFile(t)
	touch(t, dir, "encre-2026-08-01.log", 1)
	touch(t, dir, "encre-2026-08-21.log", 1)
	touch(t, dir, "encre-2026-08-22.log", 1)
	touch(t, dir, "notes.txt", 1)
	touch(t, dir, "encre-not-a-date.log", 1)

	c.advance(2 * time.Minute)
	write(t, r, "new day")

	got := names(t, dir)
	want := []string{"encre-2026-08-22.log", "encre-2026-09-21.log", "encre-not-a-date.log", "notes.txt"}
	if !slices.Equal(got, want) {
		t.Fatalf("files %v, want %v", got, want)
	}
}

func TestTheOldestFilesGoWhenThereAreTooMany(t *testing.T) {
	r, c, dir := newTestFile(t)
	for i := range maxFiles + 5 {
		touch(t, dir, fmt.Sprintf("encre-2026-09-20.%d.log", i+1), 1)
	}

	c.advance(2 * time.Minute)
	write(t, r, "new day")

	got := names(t, dir)
	if len(got) != maxFiles {
		t.Fatalf("%d files kept, want %d", len(got), maxFiles)
	}
	if slices.Contains(got, "encre-2026-09-20.1.log") || !slices.Contains(got, "encre-2026-09-21.log") {
		t.Fatalf("wrong files survived: %v", got)
	}
}

func TestTooManyFilesWithinADayArePrunedOnRollover(t *testing.T) {
	r, _, dir := newTestFile(t)
	r.maxBytes = 16
	r.maxFiles = 3
	for i := range 4 {
		write(t, r, fmt.Sprintf("line-%d-x", i))
	}

	got := names(t, dir)
	want := []string{"encre-2026-09-20.1.log", "encre-2026-09-20.2.log", "encre-2026-09-20.3.log"}
	if !slices.Equal(got, want) {
		t.Fatalf("files %v, want %v", got, want)
	}
}

func TestConcurrentWritesLoseNothing(t *testing.T) {
	r, _, dir := newTestFile(t)

	var wg sync.WaitGroup
	for g := range 10 {
		wg.Go(func() {
			for i := range 100 {
				write(t, r, fmt.Sprintf("g%d-%d", g, i))
			}
		})
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(content(t, dir, "encre-2026-09-20.log")), "\n")
	if len(lines) != 1000 {
		t.Fatalf("%d lines, want 1000", len(lines))
	}
}

func TestAMissingDirectoryIsAnErrorNotAPanic(t *testing.T) {
	r := newRotatingFile(filepath.Join(t.TempDir(), "gone"), time.Now)
	if _, err := r.Write([]byte("x")); err == nil {
		t.Fatal("write into a missing directory succeeded")
	}
}

func TestCloseCanBeCalledWithoutAWrite(t *testing.T) {
	r, _, _ := newTestFile(t)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestTheLatestLogIsTheNewestDaysHighestNumber(t *testing.T) {
	dir := t.TempDir()
	if _, err := latestLog(dir); err == nil {
		t.Fatal("an empty directory has no latest log")
	}
	for _, n := range []string{"encre-2026-09-21.log", "encre-2026-09-22.log", "encre-2026-09-22.3.log", "encre-2026-09-22.10.log"} {
		touch(t, dir, n, 1)
	}

	got, err := latestLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "encre-2026-09-22.10.log" {
		t.Fatalf("latest is %s", got)
	}
}
