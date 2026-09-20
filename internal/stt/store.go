package stt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/paradoxe35/encre/internal/logger"
	"github.com/paradoxe35/encre/internal/utils"
)

const (
	partialSuffix = ".partial"
	// A stalled TCP connection can hang without erroring; no data for this long counts as dead.
	stallTimeout  = 60 * time.Second
	progressEvery = 200 * time.Millisecond
)

type Progress struct {
	Model      Model
	Downloaded int64
	Total      int64
	BytesPerS  float64
	Stage      Stage
}

type Stage string

const (
	StageDownloading Stage = "downloading"
	StageVerifying   Stage = "verifying"
	StageDone        Stage = "done"
)

func (p Progress) Fraction() float64 {
	if p.Total <= 0 {
		return 0
	}
	return float64(p.Downloaded) / float64(p.Total)
}

type Store struct {
	dir    string
	client *http.Client
	// urlFor is a seam for tests; production always uses the pinned HF URL.
	urlFor func(Model) string

	mu       sync.Mutex
	inflight map[string]context.CancelFunc
}

func NewStore() *Store {
	return &Store{
		dir: utils.AppHomeDir("models"),
		// No overall timeout: a large model on a slow line is not an error; the stall watchdog catches dead transfers.
		client:   &http.Client{},
		urlFor:   Model.DownloadURL,
		inflight: make(map[string]context.CancelFunc),
	}
}

func (s *Store) Path(model Model) string {
	return filepath.Join(s.dir, filepath.Base(model.Filename))
}

// Size is checked because a partial rename would otherwise look complete.
func (s *Store) Downloaded(model Model) bool {
	info, err := os.Stat(s.Path(model))
	return err == nil && info.Size() == model.SizeBytes
}

// The partial file is left in place to resume from.
func (s *Store) CancelDownload(model Model) {
	s.mu.Lock()
	cancel, ok := s.inflight[model.ID]
	s.mu.Unlock()
	if ok {
		cancel()
	}
}

func (s *Store) Delete(model Model) error {
	s.CancelDownload(model)
	path := s.Path(model)

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(path + partialSuffix); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *Store) Download(ctx context.Context, model Model, report func(Progress)) error {
	if s.Downloaded(model) {
		return nil
	}

	s.mu.Lock()
	if _, busy := s.inflight[model.ID]; busy {
		s.mu.Unlock()
		return fmt.Errorf("%s is already downloading", model.Name)
	}
	ctx, cancel := context.WithCancel(ctx)
	s.inflight[model.ID] = cancel
	s.mu.Unlock()

	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.inflight, model.ID)
		s.mu.Unlock()
	}()

	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return fmt.Errorf("failed to create the model directory: %w", err)
	}

	partial := s.Path(model) + partialSuffix
	if err := s.fetch(ctx, model, partial, report); err != nil {
		return err
	}

	emit(report, Progress{Model: model, Downloaded: model.SizeBytes,
		Total: model.SizeBytes, Stage: StageVerifying})

	if err := verify(partial, model.SHA256); err != nil {
		// A corrupt partial would otherwise be resumed forever.
		os.Remove(partial)
		return err
	}

	if err := os.Rename(partial, s.Path(model)); err != nil {
		return fmt.Errorf("failed to move the model into place: %w", err)
	}

	emit(report, Progress{Model: model, Downloaded: model.SizeBytes,
		Total: model.SizeBytes, Stage: StageDone})
	logger.Info("Model downloaded", "model", model.Name)
	return nil
}

func (s *Store) fetch(ctx context.Context, model Model, partial string, report func(Progress)) error {
	resumeFrom := int64(0)
	if info, err := os.Stat(partial); err == nil {
		resumeFrom = info.Size()
	}

	// A partial already at full size needs verifying, not re-fetching, or a bad checksum loops forever.
	if resumeFrom == model.SizeBytes {
		return nil
	}
	if resumeFrom > model.SizeBytes {
		resumeFrom = 0
		os.Remove(partial)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.urlFor(model), nil)
	if err != nil {
		return err
	}
	if resumeFrom > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", resumeFrom))
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach %s: %w", model.Repo, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// The server ignored Range and is sending from the start.
		resumeFrom = 0
	case http.StatusPartialContent:
	case http.StatusRequestedRangeNotSatisfiable:
		return nil // already complete; fall through to verification
	default:
		return fmt.Errorf("%s returned %s", model.Repo, resp.Status)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if resumeFrom > 0 {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}

	file, err := os.OpenFile(partial, flags, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	return s.copy(ctx, file, resp.Body, model, resumeFrom, report)
}

func (s *Store) copy(ctx context.Context, dst io.Writer, src io.Reader,
	model Model, resumeFrom int64, report func(Progress)) error {

	buf := make([]byte, 256*1024)
	written := resumeFrom
	started := time.Now()
	lastReport := time.Now()
	lastData := time.Now()

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		n, readErr := src.Read(buf)
		if n > 0 {
			// Refuse a server sending more than it promised, rather than growing the file past its declared size.
			if model.SizeBytes > 0 && written+int64(n) > model.SizeBytes {
				n = int(model.SizeBytes - written)
			}
			if n > 0 {
				if _, err := dst.Write(buf[:n]); err != nil {
					return err
				}
				written += int64(n)
				lastData = time.Now()
			}
		}

		if time.Since(lastReport) >= progressEvery {
			emit(report, Progress{
				Model:      model,
				Downloaded: written,
				Total:      model.SizeBytes,
				BytesPerS:  float64(written-resumeFrom) / time.Since(started).Seconds(),
				Stage:      StageDownloading,
			})
			lastReport = time.Now()
		}

		if written >= model.SizeBytes && model.SizeBytes > 0 {
			return nil
		}

		if readErr == io.EOF {
			if model.SizeBytes > 0 && written < model.SizeBytes {
				return fmt.Errorf("download ended early: %d of %d bytes", written, model.SizeBytes)
			}
			return nil
		}
		if readErr != nil {
			return readErr
		}

		if time.Since(lastData) > stallTimeout {
			return errors.New("download stalled")
		}
	}
}

func verify(path, want string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return err
	}

	got := hex.EncodeToString(digest.Sum(nil))
	if !strings.EqualFold(got, want) {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", want[:12], got[:12])
	}
	return nil
}

func emit(report func(Progress), progress Progress) {
	if report != nil {
		report(progress)
	}
}
