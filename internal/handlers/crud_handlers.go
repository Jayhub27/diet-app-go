package handlers

import (
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/orionryan/diet-app-go/internal/auth"
)

// HTMX requests set the HX-Request header. For form posts (e.g. adding a
// pantry item) we return just the new <li> so hx-swap="beforeend" can append
// it without a full page reload.
func isHTMX(c echo.Context) bool {
	return c.Request().Header.Get("HX-Request") == "true"
}

func formFloat(c echo.Context, key string) float64 {
	v, _ := strconv.ParseFloat(c.FormValue(key), 64)
	return v
}

// --- Pantry ---

func (h *Handlers) Pantry(c echo.Context) error {
	u := auth.CurrentUser(c)
	items, err := h.loadPantry(c.Request().Context(), u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return h.page(c, "pantry", map[string]any{"Items": items})
}

func (h *Handlers) PantryAdd(c echo.Context) error {
	u := auth.CurrentUser(c)
	name := c.FormValue("name")
	if name == "" {
		return c.NoContent(http.StatusBadRequest)
	}
	id := uuid.NewString()
	_, err := h.DB.ExecContext(c.Request().Context(),
		`insert into pantry (id, user_id, name, amount, unit) values (?, ?, ?, ?, ?)`,
		id, u.ID, name, formFloat(c, "amount"), c.FormValue("unit"))
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	// Render just the new <li>.
	return h.Templates.RenderPartial(c.Response().Writer, "partials/pantry_item.html", map[string]any{
		"ID":     id,
		"Name":   name,
		"Amount": formFloat(c, "amount"),
		"Unit":   c.FormValue("unit"),
	})
}

func (h *Handlers) PantryDelete(c echo.Context) error {
	u := auth.CurrentUser(c)
	id := c.Param("id")
	_, err := h.DB.ExecContext(c.Request().Context(),
		`delete from pantry where id = ? and user_id = ?`, id, u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.NoContent(http.StatusOK)
}

// --- Library ---

func (h *Handlers) Library(c echo.Context) error {
	u := auth.CurrentUser(c)
	recipes, err := h.loadRecipes(c.Request().Context(), u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return h.page(c, "library", map[string]any{"Recipes": recipes})
}

// RecipeDetail renders a single saved recipe (ingredients, steps, nutrition).
func (h *Handlers) RecipeDetail(c echo.Context) error {
	u := auth.CurrentUser(c)
	r, err := h.loadRecipe(c.Request().Context(), u.ID, c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if r == nil {
		return c.String(http.StatusNotFound, "Recipe not found")
	}
	return h.page(c, "recipe", map[string]any{"Recipe": r})
}

// RecipeDelete removes a recipe. HTMX requests get a 200 so the row can be
// swapped out; full-page navigation is redirected back to the library.
func (h *Handlers) RecipeDelete(c echo.Context) error {
	u := auth.CurrentUser(c)
	_, err := h.DB.ExecContext(c.Request().Context(),
		`delete from recipes where id = ? and user_id = ?`, c.Param("id"), u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if isHTMX(c) {
		return c.NoContent(http.StatusOK)
	}
	return c.Redirect(http.StatusSeeOther, "/library")
}

// --- Profile (restrictions, blacklist, settings) ---

func (h *Handlers) Profile(c echo.Context) error {
	u := auth.CurrentUser(c)
	rs, err := h.loadRestrictions(c.Request().Context(), u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	bl, err := h.loadBlacklist(c.Request().Context(), u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	settings, err := h.loadSettings(c.Request().Context(), u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return h.page(c, "profile", map[string]any{
		"Restrictions": rs,
		"Blacklist":    bl,
		"Settings":     settings,
	})
}

func (h *Handlers) RestrictionAdd(c echo.Context) error {
	u := auth.CurrentUser(c)
	name := c.FormValue("restriction")
	severity := c.FormValue("severity")
	if name == "" || severity == "" {
		return c.NoContent(http.StatusBadRequest)
	}
	if severity != "avoid" && severity != "intolerance" && severity != "anaphylaxis" {
		severity = "avoid"
	}
	id := uuid.NewString()
	_, err := h.DB.ExecContext(c.Request().Context(),
		`insert into restrictions (id, user_id, restriction, severity) values (?, ?, ?, ?)`,
		id, u.ID, name, severity)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return h.Templates.RenderPartial(c.Response().Writer, "partials/restriction_item.html", map[string]any{
		"ID":          id,
		"Restriction": name,
		"Severity":    severity,
	})
}

func (h *Handlers) RestrictionDelete(c echo.Context) error {
	u := auth.CurrentUser(c)
	id := c.Param("id")
	_, err := h.DB.ExecContext(c.Request().Context(),
		`delete from restrictions where id = ? and user_id = ?`, id, u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.NoContent(http.StatusOK)
}

func (h *Handlers) BlacklistAdd(c echo.Context) error {
	u := auth.CurrentUser(c)
	ing := c.FormValue("ingredient")
	if ing == "" {
		return c.NoContent(http.StatusBadRequest)
	}
	id := uuid.NewString()
	_, err := h.DB.ExecContext(c.Request().Context(),
		`insert into blacklist (id, user_id, ingredient) values (?, ?, ?)`,
		id, u.ID, ing)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return h.Templates.RenderPartial(c.Response().Writer, "partials/blacklist_item.html", map[string]any{
		"ID":         id,
		"Ingredient": ing,
	})
}

func (h *Handlers) BlacklistDelete(c echo.Context) error {
	u := auth.CurrentUser(c)
	id := c.Param("id")
	_, err := h.DB.ExecContext(c.Request().Context(),
		`delete from blacklist where id = ? and user_id = ?`, id, u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.NoContent(http.StatusOK)
}

func (h *Handlers) SettingsSave(c echo.Context) error {
	u := auth.CurrentUser(c)
	source := c.FormValue("source")
	modelID := c.FormValue("model_id")
	// Upsert.
	_, err := h.DB.ExecContext(c.Request().Context(),
		`insert into user_settings (user_id, source, model_id) values (?, ?, ?)
		 on conflict(user_id) do update set source = excluded.source, model_id = excluded.model_id, updated_at = datetime('now')`,
		u.ID, source, modelID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.Redirect(http.StatusSeeOther, "/profile")
}
