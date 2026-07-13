package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"golang.org/x/crypto/bcrypt"
)

const (
	SessionCookie = "dietapp_session"
	SessionTTL    = 30 * 24 * time.Hour
	UserCtxKey    = "user"
)

type User struct {
	ID               string
	Email            string
	IsAnonymous      bool
	SubscriptionTier string
}

type Service struct {
	DB             *sql.DB
	SessionSecret  []byte
	AllowAnonymous bool
}

func NewService(db *sql.DB, secret string, allowAnon bool) *Service {
	return &Service{DB: db, SessionSecret: []byte(secret), AllowAnonymous: allowAnon}
}

// HashPassword wraps bcrypt with a sane default cost.
func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func (s *Service) Register(ctx context.Context, email, password string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" || password == "" {
		return nil, errors.New("email and password are required")
	}
	if len(password) < 8 {
		return nil, errors.New("password must be at least 8 characters")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	id := uuid.NewString()
	_, err = s.DB.ExecContext(ctx,
		`insert into users (id, email, password_hash, is_anonymous) values (?, ?, ?, 0)`,
		id, email, hash)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return nil, errors.New("an account with that email already exists")
		}
		return nil, err
	}
	// Create default settings row
	_, _ = s.DB.ExecContext(ctx,
		`insert into user_settings (user_id) values (?)`, id)
	return &User{ID: id, Email: email, IsAnonymous: false, SubscriptionTier: "free"}, nil
}

func (s *Service) Login(ctx context.Context, email, password string) (*User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var u User
	var hash string
	err := s.DB.QueryRowContext(ctx,
		`select id, email, password_hash, is_anonymous, subscription_tier from users where email = ?`, email).
		Scan(&u.ID, &u.Email, &hash, &u.IsAnonymous, &u.SubscriptionTier)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("invalid email or password")
	}
	if err != nil {
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return nil, errors.New("invalid email or password")
	}
	return &u, nil
}

// CreateAnonymous mints a random user with no password; only available when
// AllowAnonymous is true. The user is then "logged in" like a normal user.
func (s *Service) CreateAnonymous(ctx context.Context) (*User, error) {
	id := uuid.NewString()
	email := "anon-" + id[:8] + "@local"
	_, err := s.DB.ExecContext(ctx,
		`insert into users (id, email, password_hash, is_anonymous) values (?, ?, '', 1)`,
		id, email)
	if err != nil {
		return nil, err
	}
	_, _ = s.DB.ExecContext(ctx, `insert into user_settings (user_id) values (?)`, id)
	return &User{ID: id, Email: email, IsAnonymous: true, SubscriptionTier: "free"}, nil
}

func (s *Service) GetUser(ctx context.Context, id string) (*User, error) {
	var u User
	err := s.DB.QueryRowContext(ctx,
		`select id, email, is_anonymous, subscription_tier from users where id = ?`, id).
		Scan(&u.ID, &u.Email, &u.IsAnonymous, &u.SubscriptionTier)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// CreateSession stores a random session id in the DB and returns a signed
// cookie value. The cookie is "id.signature" so we can detect tampering.
func (s *Service) CreateSession(ctx context.Context, userID string) (string, error) {
	id := randHex(32)
	expires := time.Now().Add(SessionTTL).UTC().Format(time.RFC3339)
	_, err := s.DB.ExecContext(ctx,
		`insert into sessions (id, user_id, expires_at) values (?, ?, ?)`,
		id, userID, expires)
	if err != nil {
		return "", err
	}
	return signSessionID(id, s.SessionSecret), nil
}

func (s *Service) DeleteSession(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `delete from sessions where id = ?`, id)
	return err
}

func (s *Service) ResolveCookie(raw string) (string, error) {
	id, ok := verifySessionID(raw, s.SessionSecret)
	if !ok {
		return "", errors.New("invalid session")
	}
	return id, nil
}

// Middleware loads the user from the session cookie. If anonymous mode is on
// and the user has no session, a temporary anonymous user is created and a
// session cookie is set on the response.
func (s *Service) Middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			ctx := c.Request().Context()
			cookie, err := c.Cookie(SessionCookie)
			if err == nil && cookie.Value != "" {
				sid, err := s.ResolveCookie(cookie.Value)
				if err == nil {
					var userID string
					var expires string
					err := s.DB.QueryRowContext(ctx,
						`select user_id, expires_at from sessions where id = ?`, sid).
						Scan(&userID, &expires)
					if err == nil {
						if t, perr := time.Parse(time.RFC3339, expires); perr == nil && t.After(time.Now()) {
							u, err := s.GetUser(ctx, userID)
							if err == nil {
								c.Set(UserCtxKey, u)
								return next(c)
							}
						}
						// expired
						_ = s.DeleteSession(ctx, sid)
					}
				}
			}

			if s.AllowAnonymous {
				u, err := s.CreateAnonymous(ctx)
				if err != nil {
					return echo.NewHTTPError(http.StatusInternalServerError, "failed to create anonymous session")
				}
				signed, err := s.CreateSession(ctx, u.ID)
				if err != nil {
					return echo.NewHTTPError(http.StatusInternalServerError, "failed to create session")
				}
				setSessionCookie(c, signed, SessionTTL)
				c.Set(UserCtxKey, u)
			}
			return next(c)
		}
	}
}

// CurrentUser pulls the user from the request context. Returns nil if missing.
func CurrentUser(c echo.Context) *User {
	v := c.Get(UserCtxKey)
	if v == nil {
		return nil
	}
	u, _ := v.(*User)
	return u
}

func setSessionCookie(c echo.Context, value string, ttl time.Duration) {
	c.SetCookie(&http.Cookie{
		Name:     SessionCookie,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(ttl),
	})
}

func ClearSessionCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     SessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
	})
}

// --- HMAC-signed session id (id.signature) ---

func signSessionID(id string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(id))
	sig := mac.Sum(nil)
	return id + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func verifySessionID(raw string, secret []byte) (string, bool) {
	parts := strings.SplitN(raw, ".", 2)
	if len(parts) != 2 {
		return "", false
	}
	id, sig := parts[0], parts[1]
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(id))
	expected := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return "", false
	}
	if !hmac.Equal(expected, got) {
		return "", false
	}
	return id, true
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// rand.Read on Linux can't fail in practice; fallback to time
		now := time.Now().UnixNano()
		for i := range b {
			b[i] = byte(now >> (i % 8 * 8))
		}
	}
	return hex.EncodeToString(b)
}

// ErrInvalidPassword is exported for handler use.
var ErrInvalidPassword = fmt.Errorf("invalid email or password")
