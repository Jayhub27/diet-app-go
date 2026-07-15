package handlers

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/orionryan/diet-app-go/internal/auth"
	"github.com/orionryan/diet-app-go/internal/localai"
)

// curatedModel is one suggestion shown on the /models page.
type curatedModel struct {
	Name       string
	ModelURL   string
	MmprojURL  string // empty for text-only
	IsVision   bool
	Note       string
}

var curatedModels = []curatedModel{
	{
		Name:     "SmolLM2-135M (text, ~100MB)",
		ModelURL: "https://huggingface.co/QuantFactory/SmolLM2-135M-GGUF/resolve/main/SmolLM2-135M.Q4_K_M.gguf",
		IsVision: false,
		Note:     "Tiny, fast. Good for low-end hardware and quick replies.",
	},
	{
		Name:     "Llama-3.2-3B-Instruct (text, ~2GB)",
		ModelURL: "https://huggingface.co/QuantFactory/Llama-3.2-3B-Instruct-GGUF/resolve/main/Llama-3.2-3B-Instruct.Q4_K_M.gguf",
		IsVision: false,
		Note:     "Higher quality text. Needs ~4GB RAM.",
	},
	{
		Name:      "Qwen2.5-VL-3B-Instruct (vision, ~2GB)",
		ModelURL:  "https://huggingface.co/QuantFactory/Qwen2.5-VL-3B-Instruct-GGUF/resolve/main/Qwen2.5-VL-3B-Instruct.Q8_0.gguf",
		MmprojURL: "https://huggingface.co/QuantFactory/Qwen2.5-VL-3B-Instruct-GGUF/resolve/main/mmproj-Qwen2.5-VL-3B-Instruct-Q8_0.gguf",
		IsVision:  true,
		Note:      "Vision-language model. Supports image input in chat.",
	},
	{
		Name:      "Moondream2 (vision, ~1.5GB)",
		ModelURL:  "https://huggingface.co/vikhyatk/moondream2-gguf/resolve/main/moondream2-text-model-f16_ct-vicuna.gguf",
		MmprojURL: "https://huggingface.co/vikhyatk/moondream2-gguf/resolve/main/moondream2-mmproj-f16-20250414.gguf",
		IsVision:  true,
		Note:      "Tiny vision model. Fast image captions.",
	},
}

// ModelsPage renders the model catalog: downloaded models, downloads in
// progress, and curated suggestions.
func (h *Handlers) ModelsPage(c echo.Context) error {
	if err := h.LocalAI.InitLibs(); err != nil {
		// Show the page with a warning; the manager might still be unusable
		// but we want users to see what's going on.
	}

	entries, err := localai.List(c.Request().Context(), h.DB)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	jobs := h.LocalAI.ActiveJobs()
	active := h.LocalAI.ActiveEntry()

	return h.page(c, "models", map[string]any{
		"Models":        entries,
		"ActiveID":      activeID(active),
		"ActiveEntry":   active,
		"Jobs":          jobs,
		"Curated":       curatedModels,
		"ModelsDir":     h.LocalAI.ModelsDir(),
		"LibsAvailable": active != nil || h.LocalAI.InitLibs() == nil,
	})
}

// ModelsDownload kicks off a download from a URL. Returns the job ID; the
// UI polls /models/download/:id for progress via SSE.
func (h *Handlers) ModelsDownload(c echo.Context) error {
	name := c.FormValue("name")
	url := c.FormValue("url")
	if name == "" || url == "" {
		return c.String(http.StatusBadRequest, "name and url are required")
	}
	jobID, err := h.LocalAI.Download(c.Request().Context(), name, url)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, map[string]string{"job_id": jobID})
}

// ModelsDownloadCurated starts a curated model download and returns the job ID.
func (h *Handlers) ModelsDownloadCurated(c echo.Context) error {
	name := c.FormValue("name")
	if name == "" {
		return c.String(http.StatusBadRequest, "name is required")
	}
	curated := findCurated(name)
	if curated == nil {
		return c.String(http.StatusNotFound, "curated model not found")
	}

	ctx := c.Request().Context()
	id := uuid.NewString()
	modelPath := filepath.Join(h.LocalAI.ModelsDir(), filenameFromHuggingFace(curated.ModelURL))
	mmprojPath := ""
	if curated.MmprojURL != "" {
		mmprojPath = filepath.Join(h.LocalAI.ModelsDir(), filenameFromHuggingFace(curated.MmprojURL))
	}

	// Create the catalog row up-front so the user can see it immediately;
	// the actual file will land on disk via the download goroutine.
	if err := localai.Insert(ctx, h.DB, localai.CatalogEntry{
		ID:         id,
		Name:       name,
		ModelPath:  modelPath,
		MmprojPath: mmprojPath,
		SourceURL:  curated.ModelURL,
		IsVision:   curated.IsVision,
	}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Kick off the model GGUF download.
	modelJobID, err := h.LocalAI.Download(ctx, name+" (model)", curated.ModelURL)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Kick off the projector download if needed.
	mmprojJobID := ""
	if curated.MmprojURL != "" {
		mmprojJobID, _ = h.LocalAI.Download(ctx, name+" (projector)", curated.MmprojURL)
	}

	return c.JSON(http.StatusOK, map[string]string{
		"job_id":      modelJobID,
		"mmproj_job":  mmprojJobID,
		"catalog_id":  id,
	})
}

// ModelsDownloadStream streams download progress to the browser as SSE.
// One event per job per tick; the frontend re-uses this to drive a
// progress bar.
func (h *Handlers) ModelsDownloadStream(c echo.Context) error {
	c.Response().Header().Set("Content-Type", "text/event-stream")
	c.Response().Header().Set("Cache-Control", "no-cache")
	c.Response().Header().Set("Connection", "keep-alive")

	flusher, ok := c.Response().Writer.(http.Flusher)
	if !ok {
		return echo.NewHTTPError(http.StatusInternalServerError, "streaming unsupported")
	}

	// Send one initial event so the client knows we're here.
	fmt.Fprintf(c.Response().Writer, "event: hello\ndata: {}\n\n")
	flusher.Flush()

	// Send the latest snapshot of every job every second. We don't actually
	// listen for changes — the UI can poll less aggressively if needed.
	ticker := make(chan struct{}, 1)
	close(ticker) // immediate first emit

	for range ticker {
		for _, j := range h.LocalAI.ActiveJobs() {
			pct := 0.0
			if j.TotalBytes > 0 {
				pct = float64(j.Done) / float64(j.TotalBytes) * 100
			}
			fmt.Fprintf(c.Response().Writer,
				"event: progress\ndata: {\"id\":%q,\"name\":%q,\"state\":%q,\"pct\":%.1f,\"done\":%d,\"total\":%d}\n\n",
				j.ID, j.Filename, j.State, pct, j.Done, j.TotalBytes)
		}
		flusher.Flush()

		// If everything is complete or errored and the client has been
		// notified at least once, we're done.
		if allJobsDone(h.LocalAI.ActiveJobs()) {
			break
		}
	}
	return nil
}

// ModelsLoad loads a catalog entry into memory and makes it the active model.
func (h *Handlers) ModelsLoad(c echo.Context) error {
	id := c.Param("id")
	if err := h.LocalAI.Load(id); err != nil {
		return c.String(http.StatusInternalServerError, err.Error())
	}
	entry, _ := localai.Get(c.Request().Context(), h.DB, id)
	return c.JSON(http.StatusOK, map[string]any{
		"loaded":  true,
		"entry":   entry,
	})
}

// ModelsUnload frees the active model from memory.
func (h *Handlers) ModelsUnload(c echo.Context) error {
	if err := h.LocalAI.Unload(); err != nil {
		return c.String(http.StatusInternalServerError, err.Error())
	}
	return c.NoContent(http.StatusOK)
}

// ModelsDelete removes a catalog row and its files on disk.
func (h *Handlers) ModelsDelete(c echo.Context) error {
	id := c.Param("id")
	ctx := c.Request().Context()
	entry, err := localai.Get(ctx, h.DB, id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if entry == nil {
		return c.NoContent(http.StatusNotFound)
	}

	// Don't delete the active model from disk; tell the user to unload it.
	active := h.LocalAI.ActiveEntry()
	if active != nil && active.ID == id {
		return c.String(http.StatusConflict, "unload the model before deleting")
	}

	if entry.ModelPath != "" {
		_ = os.Remove(entry.ModelPath)
	}
	if entry.MmprojPath != "" {
		_ = os.Remove(entry.MmprojPath)
	}
	if err := localai.Delete(ctx, h.DB, id); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.NoContent(http.StatusOK)
}

// activeEntryByID returns the ID of the active entry, or "" if none.
func activeID(e *localai.CatalogEntry) string {
	if e == nil {
		return ""
	}
	return e.ID
}

func findCurated(name string) *curatedModel {
	for i := range curatedModels {
		if curatedModels[i].Name == name {
			return &curatedModels[i]
		}
	}
	return nil
}

func filenameFromHuggingFace(u string) string {
	// HF URLs end with /resolve/main/<filename>; the filename is the last
	// path segment.
	for i := len(u) - 1; i >= 0; i-- {
		if u[i] == '/' {
			return u[i+1:]
		}
	}
	return u
}

func allJobsDone(jobs []*localai.DownloadJob) bool {
	if len(jobs) == 0 {
		return true
	}
	for _, j := range jobs {
		if j.State == "downloading" || j.State == "queued" {
			return false
		}
	}
	return true
}

// Compile-time guard.
var _ = auth.CurrentUser
