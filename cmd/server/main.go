package main

import (
	"context"
	"log"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/orionryan/diet-app-go/internal/ai/provider"
	"github.com/orionryan/diet-app-go/internal/auth"
	"github.com/orionryan/diet-app-go/internal/config"
	"github.com/orionryan/diet-app-go/internal/db"
	"github.com/orionryan/diet-app-go/internal/handlers"
	"github.com/orionryan/diet-app-go/internal/localai"
	"github.com/orionryan/diet-app-go/internal/ratelimit"
	"github.com/orionryan/diet-app-go/internal/templates"
)

func main() {
	cfg := config.Load()

	d, err := db.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer d.Close()
	if err := db.Migrate(d, "migrations"); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	tmpl, err := templates.New()
	if err != nil {
		log.Fatalf("templates: %v", err)
	}

	aiMgr, err := localai.NewManager(d, "")
	if err != nil {
		log.Fatalf("localai: %v", err)
	}
	provider.SetLocalChatFunc(func(ctx context.Context, req provider.ChatRequest) (*provider.ChatResponse, error) {
		return aiMgr.Chat(ctx, req)
	})
	provider.SetLocalChatStreamFunc(func(ctx context.Context, req provider.ChatRequest, onDelta func(provider.StreamChunk)) error {
		return aiMgr.Stream(ctx, req, onDelta)
	})

	authSvc := auth.NewService(d, cfg.SessionSecret, cfg.AllowAnonymous)
	limiter := ratelimit.New(20, 60_000) // 20 chat messages per minute per user

	deps := handlers.Deps{
		DB:        d,
		Cfg:       cfg,
		Auth:      authSvc,
		Limiter:   limiter,
		Templates: tmpl,
		LocalAI:   aiMgr,
	}
	h := handlers.New(deps)

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.GET("/healthz", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	h.Register(e)

	addr := ":" + itoa(cfg.Port)
	log.Printf("DietApp listening on %s (db=%s, anon=%v, models=%s)", addr, cfg.DatabasePath, cfg.AllowAnonymous, aiMgr.ModelsDir())
	if err := e.Start(addr); err != nil {
		log.Fatal(err)
	}
}

func itoa(n int) string {
	// avoid pulling strconv into main just for one call
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
