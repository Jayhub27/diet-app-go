package localai

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

// DownloadJob is one in-progress or completed download.
type DownloadJob struct {
	ID         string
	URL        string
	Dest       string
	Filename   string
	StartedAt  time.Time
	TotalBytes int64
	Done       int64
	State      string // "queued", "downloading", "complete", "error"
	Error      string
}

// DefaultModelsDir returns the directory where downloaded GGUF files live.
// Falls back to ~/dietapp-models if no override is set.
func DefaultModelsDir() string {
	if v := os.Getenv("LOCAL_MODELS_DIR"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "dietapp-models")
}

// Download starts a background download from a URL into a file in modelsDir.
// It returns the job ID immediately; callers poll JobStatus for progress.
func (m *Manager) Download(ctx context.Context, name, url string) (string, error) {
	if url == "" {
		return "", fmt.Errorf("url is required")
	}

	job := &DownloadJob{
		ID:        uuid.NewString(),
		URL:       url,
		StartedAt: time.Now(),
		State:     "queued",
	}

	m.mu.Lock()
	m.jobs[job.ID] = job
	m.mu.Unlock()

	go m.runDownload(job)
	return job.ID, nil
}

func (m *Manager) runDownload(job *DownloadJob) {
	defer func() {
		if r := recover(); r != nil {
			job.State = "error"
			job.Error = fmt.Sprintf("panic: %v", r)
		}
	}()

	if err := os.MkdirAll(m.modelsDir, 0o755); err != nil {
		job.State = "error"
		job.Error = fmt.Sprintf("create dir: %v", err)
		return
	}

	// Derive a filename from the URL if possible; otherwise use a UUID.
	filename := filenameFromURL(job.URL)
	if filename == "" {
		filename = uuid.NewString() + ".gguf"
	}
	dest := filepath.Join(m.modelsDir, filename)
	job.Dest = dest
	job.Filename = filename

	// Skip if already exists.
	if _, err := os.Stat(dest); err == nil {
		fi, _ := os.Stat(dest)
		if fi != nil {
			job.TotalBytes = fi.Size()
			job.Done = fi.Size()
		}
		job.State = "complete"
		log.Printf("localai: %s already exists at %s, skipping", filename, dest)
		return
	}

	// HuggingFace blob/resolve URLs serve an HTML page unless ?download=true
	// is appended; other hosts ignore the extra query param fine.
	dlURL := job.URL
	if strings.Contains(dlURL, "huggingface.co/") {
		if strings.Contains(dlURL, "?") {
			dlURL += "&download=true"
		} else {
			dlURL += "?download=true"
		}
	}

	job.State = "downloading"
	if err := httpDownload(context.Background(), dlURL, dest, job); err != nil {
		job.State = "error"
		job.Error = err.Error()
		return
	}

	job.State = "complete"
	job.Done = job.TotalBytes
	log.Printf("localai: downloaded %s -> %s", filename, dest)
}

// httpDownload fetches url into dest with byte-level progress tracking on job.
// It follows redirects and streams to a temp file, renaming on success.
func httpDownload(ctx context.Context, url, dest string, job *DownloadJob) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "diet-app-go")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("http status %d for %s", resp.StatusCode, url)
	}

	if resp.ContentLength > 0 {
		atomic.StoreInt64(&job.TotalBytes, resp.ContentLength)
	}

	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}

	pr := &progressReadCloser{rc: resp.Body, job: job}
	_, err = io.Copy(f, pr)
	closeErr := f.Close()
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("write body: %w", err)
	}
	if closeErr != nil {
		os.Remove(tmp)
		return fmt.Errorf("close file: %w", closeErr)
	}

	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

type progressReadCloser struct {
	rc  io.ReadCloser
	job *DownloadJob
}

func (r *progressReadCloser) Read(p []byte) (int, error) {
	n, err := r.rc.Read(p)
	if n > 0 {
		atomic.AddInt64(&r.job.Done, int64(n))
	}
	return n, err
}

func (r *progressReadCloser) Close() error {
	return r.rc.Close()
}

// JobStatus returns a snapshot of a download job.
func (m *Manager) JobStatus(id string) *DownloadJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.jobs[id]; ok {
		cp := *j
		cp.Done = atomic.LoadInt64(&j.Done)
		cp.TotalBytes = atomic.LoadInt64(&j.TotalBytes)
		return &cp
	}
	return nil
}

// ActiveJobs returns all known download jobs (completed ones included).
func (m *Manager) ActiveJobs() []*DownloadJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*DownloadJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		cp := *j
		cp.Done = atomic.LoadInt64(&j.Done)
		cp.TotalBytes = atomic.LoadInt64(&j.TotalBytes)
		out = append(out, &cp)
	}
	return out
}

func filenameFromURL(u string) string {
	// Strip query string, then take last path segment.
	for i := len(u) - 1; i >= 0; i-- {
		if u[i] == '?' {
			u = u[:i]
			break
		}
	}
	idx := -1
	for i := len(u) - 1; i >= 0; i-- {
		if u[i] == '/' {
			idx = i
			break
		}
	}
	if idx < 0 || idx == len(u)-1 {
		return ""
	}
	return u[idx+1:]
}
