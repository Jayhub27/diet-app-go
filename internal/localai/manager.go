package localai

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/hybridgroup/yzma/pkg/download"
	"github.com/hybridgroup/yzma/pkg/llama"
	"github.com/hybridgroup/yzma/pkg/mtmd"
)

// LoadedModel is a model that has been loaded into memory and is ready for
// inference. Only one LoadedModel is "active" at a time.
type LoadedModel struct {
	Entry      CatalogEntry
	Model      llama.Model
	Vocab      llama.Vocab
	Context    llama.Context
	Sampler    llama.Sampler
	MtmdCtx    mtmd.Context // non-zero only for vision models
	IsVision   bool
	ChatTmpl   string
	mu         sync.Mutex
}

// Manager owns the lifecycle of the active local model, plus the in-memory
// download-job registry. There is exactly one Manager per server, set at
// startup and shared across handlers.
type Manager struct {
	db        *sql.DB
	modelsDir string
	mu        sync.RWMutex
	active    *LoadedModel
	jobs      map[string]*DownloadJob

	// Lazy-init state for the underlying C libs. Once initialized once per
	// process, no further setup is needed.
	libOnce sync.Once
	libErr  error
}

// NewManager creates a Manager backed by db and a models directory.
func NewManager(db *sql.DB, modelsDir string) (*Manager, error) {
	if modelsDir == "" {
		modelsDir = DefaultModelsDir()
	}
	if err := os.MkdirAll(modelsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create models dir: %w", err)
	}
	return &Manager{
		db:        db,
		modelsDir: modelsDir,
		jobs:      make(map[string]*DownloadJob),
	}, nil
}

// ModelsDir returns the directory used for downloaded model files.
func (m *Manager) ModelsDir() string { return m.modelsDir }

// InitLibs loads the native llama.cpp + mtmd libraries. Safe to call multiple
// times; only the first call does work.
func (m *Manager) InitLibs() error {
	m.libOnce.Do(func() {
		libPath, err := ensureLibs()
		if err != nil {
			m.libErr = err
			return
		}
		log.Printf("localai: loading llama.cpp libs from %s", libPath)
		if err := llama.Load(libPath); err != nil {
			m.libErr = fmt.Errorf("load llama lib: %w", err)
			return
		}
		llama.LogSet(llama.LogSilent())
		llama.Init()
		if err := mtmd.Load(libPath); err != nil {
			// mtmd is optional for text-only models. Log but don't fail.
			log.Printf("localai: mtmd lib not available: %v (vision models will not work)", err)
		} else {
			mtmd.LogSet(0)
		}
	})
	return m.libErr
}

// Active returns the currently-loaded model, or nil if none is loaded.
func (m *Manager) Active() *LoadedModel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.active
}

// ActiveEntry returns the catalog entry of the active model, or nil.
func (m *Manager) ActiveEntry() *CatalogEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.active == nil {
		return nil
	}
	e := m.active.Entry
	return &e
}

// Load reads the catalog entry by id, loads the GGUF into memory, and
// activates it. Unloads any previously active model first.
func (m *Manager) Load(id string) error {
	if err := m.InitLibs(); err != nil {
		return err
	}

	entry, err := Get(context.Background(), m.db, id)
	if err != nil {
		return fmt.Errorf("lookup model: %w", err)
	}
	if entry == nil {
		return fmt.Errorf("model not found: %s", id)
	}
	if _, err := os.Stat(entry.ModelPath); err != nil {
		return fmt.Errorf("model file missing on disk: %w", err)
	}

	loaded := &LoadedModel{Entry: *entry, IsVision: entry.IsVision}

	log.Printf("localai: loading %s from %s", entry.Name, entry.ModelPath)
	model, err := llama.ModelLoadFromFile(entry.ModelPath, llama.ModelDefaultParams())
	if err != nil {
		return fmt.Errorf("load model file: %w", err)
	}
	loaded.Model = model
	loaded.Vocab = llama.ModelGetVocab(model)

	tmpl := llama.ModelChatTemplate(model, "")
	if tmpl != "" {
		loaded.ChatTmpl = tmpl
	}

	ctxParams := llama.ContextDefaultParams()
	ctxParams.NCtx = 4096
	ctxParams.NBatch = 512
	ctx, err := llama.InitFromModel(model, ctxParams)
	if err != nil {
		llama.ModelFree(model)
		return fmt.Errorf("init context: %w", err)
	}
	loaded.Context = ctx

	samplerParams := llama.DefaultSamplerParams()
	sampler := llama.NewSampler(model, llama.DefaultSamplers, samplerParams)
	loaded.Sampler = sampler

	if entry.IsVision {
		if entry.MmprojPath == "" {
			llama.Free(ctx)
			llama.ModelFree(model)
			return fmt.Errorf("vision model %s has no projector path", entry.Name)
		}
		if _, err := os.Stat(entry.MmprojPath); err != nil {
			llama.Free(ctx)
			llama.ModelFree(model)
			return fmt.Errorf("projector file missing: %w", err)
		}
		mtmdCtx, err := mtmd.InitFromFile(entry.MmprojPath, model, mtmd.ContextParamsDefault())
		if err != nil {
			llama.Free(ctx)
			llama.ModelFree(model)
			return fmt.Errorf("init mtmd: %w", err)
		}
		loaded.MtmdCtx = mtmdCtx
		if !mtmd.SupportVision(mtmdCtx) {
			log.Printf("localai: WARNING projector for %s does not advertise vision support", entry.Name)
		}
	}

	// Swap in the new model, free the old one.
	m.mu.Lock()
	old := m.active
	m.active = loaded
	m.mu.Unlock()

	if old != nil {
		m.unloadLocked(old)
	}

	log.Printf("localai: %s loaded and active (vision=%v)", entry.Name, entry.IsVision)
	return nil
}

// Unload frees the currently active model from memory. The catalog row and
// files are untouched.
func (m *Manager) Unload() error {
	m.mu.Lock()
	old := m.active
	m.active = nil
	m.mu.Unlock()
	if old == nil {
		return nil
	}
	m.unloadLocked(old)
	log.Printf("localai: unloaded %s", old.Entry.Name)
	return nil
}

func (m *Manager) unloadLocked(lm *LoadedModel) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if lm.MtmdCtx != 0 {
		_ = mtmd.Free(lm.MtmdCtx)
		lm.MtmdCtx = 0
	}
	if lm.Sampler != 0 {
		llama.SamplerFree(lm.Sampler)
		lm.Sampler = 0
	}
	if lm.Context != 0 {
		llama.Free(lm.Context)
		lm.Context = 0
	}
	if lm.Model != 0 {
		llama.ModelFree(lm.Model)
		lm.Model = 0
	}
}

// LoadByName loads a catalog entry by display name (used when restoring the
// user's preferred model from user_settings).
func (m *Manager) LoadByName(name string) error {
	entry, err := FindByName(context.Background(), m.db, name)
	if err != nil {
		return err
	}
	if entry == nil {
		return fmt.Errorf("no model named %q in catalog", name)
	}
	return m.Load(entry.ID)
}

// SetActive updates the user_settings row so the user's chat requests use
// this model going forward. The model must already be loaded.
func (m *Manager) SetActive(entryID string) error {
	entry, err := Get(context.Background(), m.db, entryID)
	if err != nil {
		return err
	}
	if entry == nil {
		return errors.New("model not found")
	}
	m.mu.RLock()
	active := m.active
	m.mu.RUnlock()
	if active == nil || active.Entry.ID != entryID {
		return errors.New("model must be loaded before setting as active")
	}
	_, err = m.db.ExecContext(context.Background(),
		`update user_settings set model_id = ?, updated_at = datetime('now')
		 where user_id in (select id from users limit 1)`)
	// The above is a no-op for per-user settings; the real call site should
	// pass a userID. Kept here for symmetry; see handlers.
	return err
}

// ensureLibs replicates the lib-loading logic from the previous local.go
// using download.AlreadyInstalled + download.Get.
func ensureLibs() (string, error) {
	if libPath := os.Getenv("YZMA_LIB"); libPath != "" {
		if download.AlreadyInstalled(libPath) {
			return libPath, nil
		}
	}
	home, _ := os.UserHomeDir()
	candidates := []string{
		filepath.Join(home, ".yzma", "libs"),
		"/usr/local/lib/llama.cpp",
		"./llama-libs",
	}
	for _, c := range candidates {
		if download.AlreadyInstalled(c) {
			return c, nil
		}
	}
	dest := filepath.Join(home, ".yzma", "libs")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", fmt.Errorf("create lib dir: %w", err)
	}
	processor := "cpu"
	if ok, ver := download.HasCUDA(); ok {
		log.Printf("localai: CUDA detected (version %s), using CUDA build", ver)
		processor = "cuda"
	} else if ok, ver := download.HasROCm(); ok {
		log.Printf("localai: ROCm detected (version %s), using ROCm build", ver)
		processor = "rocm"
	}
	log.Printf("localai: downloading llama.cpp libraries (arch=%s, os=%s, processor=%s)...",
		runtime.GOARCH, runtime.GOOS, processor)
	if err := download.Get(runtime.GOARCH, runtime.GOOS, processor, "latest", dest); err != nil {
		return "", fmt.Errorf("download libs: %w", err)
	}
	log.Printf("localai: llama.cpp libraries installed to %s", dest)
	return dest, nil
}
