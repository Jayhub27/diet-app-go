package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/orionryan/diet-app-go/internal/ai"
	"github.com/orionryan/diet-app-go/internal/ai/provider"
	"github.com/orionryan/diet-app-go/internal/ai/tools"
	"github.com/orionryan/diet-app-go/internal/auth"
	"github.com/orionryan/diet-app-go/internal/config"
	"github.com/orionryan/diet-app-go/internal/localai"
	"github.com/orionryan/diet-app-go/internal/ratelimit"
	"github.com/orionryan/diet-app-go/internal/templates"
)

type Deps struct {
	DB        *sql.DB
	Cfg       *config.Config
	Auth      *auth.Service
	Limiter   *ratelimit.Limiter
	Templates *templates.Engine
	LocalAI   *localai.Manager
}

type Handlers struct{ Deps }

func New(d Deps) *Handlers { return &Handlers{Deps: d} }

// Register wires every route onto the Echo instance.
func (h *Handlers) Register(e *echo.Echo) {
	e.Use(h.Auth.Middleware())

	// Public
	e.GET("/auth/login", h.LoginPage)
	e.POST("/auth/login", h.LoginSubmit)
	e.GET("/auth/register", h.RegisterPage)
	e.POST("/auth/register", h.RegisterSubmit)
	e.GET("/auth/logout", h.Logout)
	e.POST("/auth/logout", h.Logout)

	// Static
	e.Static("/static", "static")

	// App
	e.GET("/", h.Chat)
	e.POST("/chat", h.ChatSend) // HTMX form post
	e.GET("/pantry", h.Pantry)
	e.POST("/pantry", h.PantryAdd)
	e.DELETE("/pantry/:id", h.PantryDelete)
	e.GET("/library", h.Library)
	e.GET("/library/:id", h.RecipeDetail)
	e.DELETE("/library/:id", h.RecipeDelete)
	e.GET("/profile", h.Profile)
	e.POST("/restrictions", h.RestrictionAdd)
	e.DELETE("/restrictions/:id", h.RestrictionDelete)
	e.POST("/blacklist", h.BlacklistAdd)
	e.DELETE("/blacklist/:id", h.BlacklistDelete)
	e.POST("/settings", h.SettingsSave)

	// Local AI model management.
	e.GET("/models", h.ModelsPage)
	e.POST("/models/download", h.ModelsDownload)
	e.POST("/models/download/curated", h.ModelsDownloadCurated)
	e.GET("/models/download/stream", h.ModelsDownloadStream)
	e.POST("/models/:id/load", h.ModelsLoad)
	e.POST("/models/:id/unload", h.ModelsUnload)
	e.DELETE("/models/:id", h.ModelsDelete)
}

// --- pages ---

func (h *Handlers) page(c echo.Context, name string, data any) error {
	// Theme is stored in localStorage on the client; we just always render
	// the light class on <html>, and the inline script applies .dark if
	// the saved preference says so. The DarkMode field is here in case
	// we later want to render with a specific theme server-side.
	d := map[string]any{
		"Title":    titleFor(name),
		"User":     auth.CurrentUser(c),
		"DarkMode": false,
	}
	for k, v := range asMap(data) {
		d[k] = v
	}
	return h.Templates.RenderPage(c.Response().Writer, name, d)
}

func titleFor(name string) string {
	switch name {
	case "chat":
		return "Chat"
	case "pantry":
		return "Pantry"
	case "library":
		return "Library"
	case "profile":
		return "Profile"
	case "login":
		return "Sign in"
	case "register":
		return "Create account"
	}
	return "DietApp"
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

// --- chat ---

type chatMessage struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

func (h *Handlers) Chat(c echo.Context) error {
	u := auth.CurrentUser(c)
	ctx := c.Request().Context()
	convID, msgs, err := h.loadOrCreateConversation(ctx, u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	c.Response().Header().Set("X-Conversation-Id", convID)
	return h.page(c, "chat", map[string]any{
		"Messages": msgs,
		"ConvID":   convID,
	})
}

func (h *Handlers) loadOrCreateConversation(ctx context.Context, userID string) (string, []chatMessage, error) {
	var convID string
	err := h.DB.QueryRowContext(ctx,
		`select id from conversations where user_id = ? order by updated_at desc limit 1`, userID).Scan(&convID)
	if errors.Is(err, sql.ErrNoRows) {
		convID = uuid.NewString()
		_, err = h.DB.ExecContext(ctx,
			`insert into conversations (id, user_id, title) values (?, ?, 'New chat')`, convID, userID)
		if err != nil {
			return "", nil, err
		}
		return convID, nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	rows, err := h.DB.QueryContext(ctx,
		`select id, role, content, created_at from messages where conversation_id = ? order by created_at asc`, convID)
	if err != nil {
		return "", nil, err
	}
	defer rows.Close()
	var out []chatMessage
	for rows.Next() {
		var m chatMessage
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return "", nil, err
		}
		out = append(out, m)
	}
	return convID, out, nil
}

func (h *Handlers) ChatSend(c echo.Context) error {
	u := auth.CurrentUser(c)
	if u == nil {
		return echo.NewHTTPError(http.StatusUnauthorized, "sign in first")
	}

	// Rate limit.
	rl := h.Limiter.Allow("chat:" + u.ID)
	if !rl.Allowed {
		return c.String(http.StatusTooManyRequests, "You're going a bit fast — try again in a few seconds.")
	}

	userText := strings.TrimSpace(c.FormValue("message"))
	if userText == "" {
		return c.NoContent(http.StatusBadRequest)
	}

	// Accept multipart image uploads (vision path). The images are saved to
	// a temp dir, passed to the vision tool, then removed.
	var imagePaths []string
	form, ferr := c.MultipartForm()
	if ferr == nil && form != nil {
		files := form.File["images"]
		if len(files) > 0 {
			tmpDir, err := os.MkdirTemp("", "dietapp-img-*")
			if err != nil {
				return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
			}
			defer os.RemoveAll(tmpDir)
			for _, fh := range files {
				if fh.Size > 25*1024*1024 {
					continue
				}
				src, err := fh.Open()
				if err != nil {
					continue
				}
				dst, err := os.Create(filepath.Join(tmpDir, filepath.Base(fh.Filename)))
				if err != nil {
					src.Close()
					continue
				}
				if _, err := io.Copy(dst, src); err != nil {
					src.Close()
					dst.Close()
					continue
				}
				src.Close()
				dst.Close()
				imagePaths = append(imagePaths, dst.Name())
			}
		}
	}

	ctx := c.Request().Context()
	convID, _, err := h.loadOrCreateConversation(ctx, u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Persist user message (mention attached images in transcript).
	persistedUserText := userText
	if len(imagePaths) > 0 {
		persistedUserText = "[attached " + fmtInt(len(imagePaths)) + " image(s)]\n" + userText
	}
	userMsgID := uuid.NewString()
	_, err = h.DB.ExecContext(ctx,
		`insert into messages (id, conversation_id, user_id, role, content) values (?, ?, ?, 'user', ?)`,
		userMsgID, convID, u.ID, persistedUserText)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Load user context (pantry, restrictions, blacklist) and settings.
	settings, err := h.loadSettings(ctx, u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	pantry, err := h.loadPantry(ctx, u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	restrictions, err := h.loadRestrictions(ctx, u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	blacklist, err := h.loadBlacklist(ctx, u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Build provider + agent.
	src := provider.Source(settings.Source)
	if src == "" {
		src = provider.Source(h.Cfg.DefaultAISource)
	}
	model := settings.ModelID
	if model == "" {
		model = h.Cfg.DefaultAIModel
	}
	apiKey := settings.APIKey
	if apiKey == "" {
		apiKey = h.providerDefaultKey(src)
	}
	baseURL := settings.BaseURL
	if baseURL == "" {
		baseURL = h.providerDefaultBaseURL(src)
	}

	// If using the local source AND the user attached images AND the active
	// model is vision-capable, bypass the agent's tool loop and call the
	// manager's vision path directly.
	if src == provider.SourceLocal && len(imagePaths) > 0 && h.LocalAI != nil && h.LocalAI.IsVision() {
		reply, vErr := h.runVisionLocal(ctx, userText, imagePaths, pantry, restrictions, blacklist)
		if vErr != nil {
			return h.renderChatBubble(c, persistedUserText, "Sorry — the AI call failed: "+vErr.Error(), nil)
		}
		h.persistAssistant(ctx, convID, u.ID, reply)
		return h.renderChatBubble(c, persistedUserText, reply, nil)
	}

	// Local source with no images: just use the provider.Client path —
	// the local hook is wired at startup, so source=local transparently
	// dispatches to the manager.
	client := provider.New(provider.Options{
		Source:    src,
		Model:     model,
		APIKey:    apiKey,
		BaseURL:   baseURL,
		AppURL:    h.Cfg.OpenRouterAppURL,
		AppName:   h.Cfg.OpenRouterAppName,
		ModelPath: h.Cfg.LocalModelPath,
	})

	// Build the tool-call context (typed for the tools package).
	rs := make([]tools.Restriction, 0, len(restrictions))
	for _, r := range restrictions {
		rs = append(rs, tools.Restriction{Name: r.Restriction, Severity: tools.Severity(r.Severity)})
	}
	pantryView := make([]map[string]any, 0, len(pantry))
	for _, p := range pantry {
		pantryView = append(pantryView, map[string]any{
			"name": p.Name, "amount": p.Amount, "unit": p.Unit,
		})
	}
	bl := make([]string, 0, len(blacklist))
	for _, b := range blacklist {
		bl = append(bl, b.Ingredient)
	}

	// All agent tools (mirrors the OG tool set). Heavy tools that make their
	// own LLM calls reuse the same provider client; DB-backed tools get the
	// connection and user id.
	agentTools := []*tools.Tool{
		tools.ComputeNutrition(),
		tools.GenerateRecipe(),
		tools.SearchIngredients(),
		tools.SearchLocations(),
		tools.ApplyRestrictions(rs),
		tools.AddToPantry(h.DB, u.ID),
		tools.RemoveFromPantry(h.DB, u.ID),
		tools.ListPantry(h.DB, u.ID),
		tools.SaveRecipeToLibrary(h.DB, u.ID),
		tools.BuildGroceryList(),
		tools.ValidateIngredientsDeep(client),
		tools.PlanWeek(client),
	}

	// Recent history for the model (last 20 messages).
	history, err := h.recentMessages(ctx, convID, 20)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Build system prompt with user context appended.
	sys := ai.SystemPrompt
	sys += ai.BuildContextBlock(pantryView, rs, bl)

	provMsgs := make([]provider.Message, 0, len(history)+1)
	for _, m := range history {
		provMsgs = append(provMsgs, provider.Message{Role: m.Role, Content: m.Content})
	}
	provMsgs = append(provMsgs, provider.Message{Role: "user", Content: userText})

	agent := &ai.Agent{Client: client, Model: model, Tools: agentTools, MaxSteps: 6}
	res, err := agent.Run(ctx, ai.RunOptions{
		System:   sys,
		Messages: provMsgs,
	})
	if err != nil {
		// Persist an error reply so the user sees something.
		_, _ = h.DB.ExecContext(ctx,
			`insert into messages (id, conversation_id, user_id, role, content) values (?, ?, ?, 'assistant', ?)`,
			uuid.NewString(), convID, u.ID, "Sorry — the AI call failed: "+err.Error())
		// Render the user message plus an error bubble via HTMX.
		return h.renderChatBubble(c, persistedUserText, "Sorry — the AI call failed: "+err.Error(), nil)
	}
	reply := res.Reply
	if reply == "" {
		reply = "(no reply)"
	}
	h.persistAssistant(ctx, convID, u.ID, reply)

	// Optional: record tool hit list for the UI.
	toolHits := make([]map[string]string, 0)
	for name, count := range res.ToolHit {
		toolHits = append(toolHits, map[string]string{
			"Name":   name,
			"Result": fmt.Sprintf("%d call(s)", count),
		})
	}
	return h.renderChatBubble(c, persistedUserText, reply, toolHits)
}

// runVisionLocal calls the local manager's vision path with a single
// user-message + system context. No tool loop.
func (h *Handlers) runVisionLocal(ctx context.Context, userText string, imagePaths []string,
	pantry []pantryRow, restrictions []restrictionRow, blacklist []blacklistRow) (string, error) {

	rs := make([]tools.Restriction, 0, len(restrictions))
	for _, r := range restrictions {
		rs = append(rs, tools.Restriction{Name: r.Restriction, Severity: tools.Severity(r.Severity)})
	}
	pantryView := make([]map[string]any, 0, len(pantry))
	for _, p := range pantry {
		pantryView = append(pantryView, map[string]any{"name": p.Name, "amount": p.Amount, "unit": p.Unit})
	}
	bl := make([]string, 0, len(blacklist))
	for _, b := range blacklist {
		bl = append(bl, b.Ingredient)
	}
	sys := ai.SystemPrompt + ai.BuildContextBlock(pantryView, rs, bl)

	msgs := []provider.Message{
		{Role: "system", Content: sys},
		{Role: "user", Content: userText},
	}
	resp, err := h.LocalAI.ChatVision(ctx, provider.ChatRequest{Messages: msgs}, imagePaths)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "(no reply)", nil
	}
	return resp.Choices[0].Message.Content, nil
}

func (h *Handlers) persistAssistant(ctx context.Context, convID, userID, reply string) {
	_, _ = h.DB.ExecContext(ctx,
		`insert into messages (id, conversation_id, user_id, role, content) values (?, ?, ?, 'assistant', ?)`,
		uuid.NewString(), convID, userID, reply)
	_, _ = h.DB.ExecContext(ctx, `update conversations set updated_at = datetime('now') where id = ?`, convID)
}

func fmtInt(n int) string {
	return strconvItoa(n)
}

func strconvItoa(n int) string {
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

func (h *Handlers) renderChatBubble(c echo.Context, userText, reply string, toolHits []map[string]string) error {
	// HTMX appends the response to #chat-log. The user message is in the form
	// input (it just got submitted), so we render user + assistant bubbles.
	w := c.Response().Writer
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.Templates.RenderPartial(w, "partials/user_message.html", map[string]any{"Content": userText}); err != nil {
		return err
	}
	_, _ = w.Write([]byte("\n"))
	return h.Templates.RenderPartial(w, "partials/assistant_message.html", map[string]any{
		"Content": reply,
		"Tools":   toolHits,
	})
}

// --- helpers ---

type pantryRow struct {
	ID     string
	Name   string
	Amount float64
	Unit   string
}

func (h *Handlers) loadPantry(ctx context.Context, userID string) ([]pantryRow, error) {
	rows, err := h.DB.QueryContext(ctx,
		`select id, name, coalesce(amount, 0), coalesce(unit, '') from pantry where user_id = ? order by created_at desc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pantryRow
	for rows.Next() {
		var p pantryRow
		if err := rows.Scan(&p.ID, &p.Name, &p.Amount, &p.Unit); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

type restrictionRow struct {
	ID          string
	Restriction string
	Severity    string
}

func (h *Handlers) loadRestrictions(ctx context.Context, userID string) ([]restrictionRow, error) {
	rows, err := h.DB.QueryContext(ctx,
		`select id, restriction, severity from restrictions where user_id = ? order by created_at desc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []restrictionRow
	for rows.Next() {
		var r restrictionRow
		if err := rows.Scan(&r.ID, &r.Restriction, &r.Severity); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

type blacklistRow struct {
	ID         string
	Ingredient string
}

func (h *Handlers) loadBlacklist(ctx context.Context, userID string) ([]blacklistRow, error) {
	rows, err := h.DB.QueryContext(ctx,
		`select id, ingredient from blacklist where user_id = ? order by created_at desc`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []blacklistRow
	for rows.Next() {
		var b blacklistRow
		if err := rows.Scan(&b.ID, &b.Ingredient); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

type settingsRow struct {
	Source  string
	ModelID string
	BaseURL string
	APIKey  string
}

func (h *Handlers) loadSettings(ctx context.Context, userID string) (settingsRow, error) {
	var s settingsRow
	err := h.DB.QueryRowContext(ctx,
		`select source, model_id, coalesce(base_url, ''), coalesce(api_key, '') from user_settings where user_id = ?`, userID,
	).Scan(&s.Source, &s.ModelID, &s.BaseURL, &s.APIKey)
	if errors.Is(err, sql.ErrNoRows) {
		// Lazy-create with defaults.
		s = settingsRow{
			Source:  h.Cfg.DefaultAISource,
			ModelID: h.Cfg.DefaultAIModel,
		}
		_, _ = h.DB.ExecContext(ctx,
			`insert into user_settings (user_id, source, model_id) values (?, ?, ?)`,
			userID, s.Source, s.ModelID)
		return s, nil
	}
	return s, err
}

func (h *Handlers) loadRecipes(ctx context.Context, userID string) ([]recipeRow, error) {
	rows, err := h.DB.QueryContext(ctx,
		`select id, title, coalesce(description, ''), servings, prep_minutes, cook_minutes
		 from recipes where user_id = ? order by created_at desc limit 100`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recipeRow
	for rows.Next() {
		var r recipeRow
		if err := rows.Scan(&r.ID, &r.Title, &r.Description, &r.Servings, &r.PrepMinutes, &r.CookMinutes); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

type recipeRow struct {
	ID, Title, Description             string
	Servings, PrepMinutes, CookMinutes int
}

// fullRecipe is the complete recipe loaded for the detail page. JSON columns
// are unmarshalled into typed slices/maps so the template can render them.
type fullRecipe struct {
	ID           string
	Title        string
	Description  string
	Servings     int
	PrepMinutes  int
	CookMinutes  int
	Ingredients  []ingredientView
	Steps        []string
	DietaryTags  []string
	Nutrition    nutritionView
	SourcePrompt string
}

type ingredientView struct {
	Name   string  `json:"name"`
	Amount float64 `json:"amount"`
	Unit   string  `json:"unit"`
}

type nutritionView struct {
	PerServing macroView `json:"perServing"`
	Total      macroView `json:"total"`
}

type macroView struct {
	Calories     float64 `json:"calories"`
	ProteinGrams float64 `json:"proteinGrams"`
	CarbsGrams   float64 `json:"carbsGrams"`
	FatGrams     float64 `json:"fatGrams"`
}

// loadRecipe fetches a single recipe owned by the user, including all JSON
// columns (ingredients, steps, tags, nutrition). Returns nil if not found.
func (h *Handlers) loadRecipe(ctx context.Context, userID, id string) (*fullRecipe, error) {
	var r fullRecipe
	var ingredientsJSON, stepsJSON, tagsJSON, nutritionJSON, sourcePrompt sql.NullString
	err := h.DB.QueryRowContext(ctx,
		`select id, title, coalesce(description, ''), servings, prep_minutes, cook_minutes,
		        ingredients_json, steps_json, dietary_tags_json, nutrition_json, coalesce(source_prompt, '')
		 from recipes where id = ? and user_id = ?`,
		id, userID,
	).Scan(&r.ID, &r.Title, &r.Description, &r.Servings, &r.PrepMinutes, &r.CookMinutes,
		&ingredientsJSON, &stepsJSON, &tagsJSON, &nutritionJSON, &sourcePrompt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.SourcePrompt = sourcePrompt.String
	_ = json.Unmarshal([]byte(ingredientsJSON.String), &r.Ingredients)
	_ = json.Unmarshal([]byte(stepsJSON.String), &r.Steps)
	_ = json.Unmarshal([]byte(tagsJSON.String), &r.DietaryTags)
	_ = json.Unmarshal([]byte(nutritionJSON.String), &r.Nutrition)
	return &r, nil
}

func (h *Handlers) recentMessages(ctx context.Context, convID string, n int) ([]chatMessage, error) {
	rows, err := h.DB.QueryContext(ctx,
		`select id, role, content, created_at from messages
		 where conversation_id = ? and role in ('user', 'assistant')
		 order by created_at desc limit ?`, convID, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []chatMessage
	for rows.Next() {
		var m chatMessage
		if err := rows.Scan(&m.ID, &m.Role, &m.Content, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (h *Handlers) providerDefaultKey(s provider.Source) string {
	switch s {
	case provider.SourceOpenAI:
		return h.Cfg.OpenAIKey
	case provider.SourceOpenRouter:
		return h.Cfg.OpenRouterKey
	}
	return ""
}

func (h *Handlers) providerDefaultBaseURL(s provider.Source) string {
	switch s {
	case provider.SourceOllama:
		return h.Cfg.OllamaBaseURL
	case provider.SourceLMStudio:
		return h.Cfg.LMStudioBaseURL
	}
	return ""
}
