package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/orionryan/diet-app-go/internal/ai/tools"
	"github.com/orionryan/diet-app-go/internal/auth"
)

type groceryView struct {
	Groups    []tools.GroceryCategory
	Total     int
	Checked   int
	Remaining int
	WeekStart string
}

func (h *Handlers) buildGroceryView(ctx context.Context, userID string) (*groceryView, error) {
	weekStart := tools.CurrentWeekStart()
	items, err := tools.LoadGroceryList(ctx, h.DB, userID, weekStart)
	if err != nil {
		return nil, err
	}
	checked := 0
	for _, item := range items {
		if item.Checked {
			checked++
		}
	}
	return &groceryView{
		Groups:    tools.GroupGroceryList(items),
		Total:     len(items),
		Checked:   checked,
		Remaining: len(items) - checked,
		WeekStart: weekStart,
	}, nil
}

func (v *groceryView) templateData() map[string]any {
	return map[string]any{
		"Groups":    v.Groups,
		"Total":     v.Total,
		"Checked":   v.Checked,
		"Remaining": v.Remaining,
		"WeekStart": v.WeekStart,
	}
}

// Groceries renders this week's shopping list.
func (h *Handlers) Groceries(c echo.Context) error {
	u := auth.CurrentUser(c)
	view, err := h.buildGroceryView(c.Request().Context(), u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return h.page(c, "groceries", view.templateData())
}

// renderGroceryList returns the list fragment for HTMX mutations.
func (h *Handlers) renderGroceryList(c echo.Context, u *auth.User) error {
	view, err := h.buildGroceryView(c.Request().Context(), u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return h.Templates.RenderPartial(c.Response().Writer, "partials/grocery_list.html", view.templateData())
}

func (h *Handlers) groceryDone(c echo.Context, u *auth.User) error {
	if isHTMX(c) {
		return h.renderGroceryList(c, u)
	}
	return c.Redirect(http.StatusSeeOther, "/groceries")
}

func (h *Handlers) GroceryAdd(c echo.Context) error {
	u := auth.CurrentUser(c)
	name := strings.TrimSpace(c.FormValue("name"))
	if name == "" {
		return c.NoContent(http.StatusBadRequest)
	}
	amount, _ := strconv.ParseFloat(c.FormValue("amount"), 64)
	item := tools.GroceryItem{
		Name:   name,
		Amount: amount,
		Unit:   strings.TrimSpace(c.FormValue("unit")),
	}
	_, _, err := tools.AddGroceryItems(c.Request().Context(), h.DB, u.ID, []tools.GroceryItem{item}, "Added by you")
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return h.groceryDone(c, u)
}

func (h *Handlers) GroceryToggle(c echo.Context) error {
	u := auth.CurrentUser(c)
	name := strings.TrimSpace(c.FormValue("name"))
	if name == "" {
		return c.NoContent(http.StatusBadRequest)
	}
	checked := c.FormValue("checked") == "true"
	if _, err := tools.CheckGroceryItems(c.Request().Context(), h.DB, u.ID, []string{name}, checked); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return h.groceryDone(c, u)
}

func (h *Handlers) GroceryRemove(c echo.Context) error {
	u := auth.CurrentUser(c)
	name := strings.TrimSpace(c.FormValue("name"))
	if name == "" {
		return c.NoContent(http.StatusBadRequest)
	}
	if _, err := tools.RemoveGroceryItems(c.Request().Context(), h.DB, u.ID, []string{name}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return h.groceryDone(c, u)
}

func (h *Handlers) GroceryClear(c echo.Context) error {
	u := auth.CurrentUser(c)
	if err := tools.ClearGroceryList(c.Request().Context(), h.DB, u.ID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return h.groceryDone(c, u)
}

// GroceryClearChecked drops everything already marked as bought.
func (h *Handlers) GroceryClearChecked(c echo.Context) error {
	u := auth.CurrentUser(c)
	ctx := c.Request().Context()
	items, err := tools.LoadGroceryList(ctx, h.DB, u.ID, tools.CurrentWeekStart())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	remaining := make([]tools.GroceryItem, 0, len(items))
	for _, item := range items {
		if !item.Checked {
			remaining = append(remaining, item)
		}
	}
	if err := tools.SaveGroceryList(ctx, h.DB, u.ID, tools.CurrentWeekStart(), remaining); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return h.groceryDone(c, u)
}
