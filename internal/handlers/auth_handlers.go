package handlers

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/orionryan/diet-app-go/internal/auth"
)

func (h *Handlers) LoginPage(c echo.Context) error {
	return h.page(c, "login", nil)
}

func (h *Handlers) LoginSubmit(c echo.Context) error {
	email := c.FormValue("email")
	password := c.FormValue("password")
	u, err := h.Auth.Login(c.Request().Context(), email, password)
	if err != nil {
		return h.page(c, "login", map[string]any{"Error": err.Error()})
	}
	signed, err := h.Auth.CreateSession(c.Request().Context(), u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	c.SetCookie(&http.Cookie{
		Name:     auth.SessionCookie,
		Value:    signed,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(auth.SessionTTL),
	})
	return c.Redirect(http.StatusSeeOther, "/")
}

func (h *Handlers) RegisterPage(c echo.Context) error {
	return h.page(c, "register", nil)
}

func (h *Handlers) RegisterSubmit(c echo.Context) error {
	email := c.FormValue("email")
	password := c.FormValue("password")
	u, err := h.Auth.Register(c.Request().Context(), email, password)
	if err != nil {
		return h.page(c, "register", map[string]any{"Error": err.Error()})
	}
	signed, err := h.Auth.CreateSession(c.Request().Context(), u.ID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	c.SetCookie(&http.Cookie{
		Name:     auth.SessionCookie,
		Value:    signed,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(auth.SessionTTL),
	})
	return c.Redirect(http.StatusSeeOther, "/")
}

func (h *Handlers) Logout(c echo.Context) error {
	cookie, err := c.Cookie(auth.SessionCookie)
	if err == nil && cookie.Value != "" {
		if sid, err := h.Auth.ResolveCookie(cookie.Value); err == nil {
			_ = h.Auth.DeleteSession(c.Request().Context(), sid)
		}
	}
	auth.ClearSessionCookie(c)
	return c.Redirect(http.StatusSeeOther, "/")
}
