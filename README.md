# DietApp (Go + HTMX + SQLite)

An app for people with restrictive diets: meal planning from what's already in
the kitchen, real-time label and barcode checks, and real products checked
against your restrictions. Free with local AI — single Go binary, SQLite file,
HTMX, and an OpenAI-compatible agent (OpenAI, OpenRouter, Ollama, LM Studio).

## Goals

- One binary, one SQLite file, zero JS build step.
- Same features as the MVP slice: chat, pantry, library, profile (restrictions,
  blacklist, AI settings), email+password auth (with anonymous fallback).
- Streaming chat is a follow-up; MVP is non-streaming (full reply at the end).

## Layout

```
cmd/server/main.go            # entry point
internal/
  config/                     # env loading
  db/                         # sqlite open + migration runner
  auth/                       # bcrypt, sessions, middleware
  ratelimit/                  # in-memory sliding-window limiter
  ai/provider/                # OpenAI-compatible client
  ai/tools/                   # compute_nutrition, search_ingredients, apply_restrictions,
                              # find_products + lookup_product_by_barcode (Open Food Facts)
  ai/agent.go                 # tool-calling loop
  handlers/                   # echo handlers (auth, chat, crud)
  templates/                  # html/template engine
migrations/0001_init.sql      # schema
views/                        # html templates
static/css/                   # handcrafted CSS (no Tailwind build)
```

## Setup

```bash
# 1. Install Go 1.23+ if you don't have it:
#    https://go.dev/dl/
go version

# 2. Copy env and fill in your API keys
cp .env.example .env
$EDITOR .env   # set OPENROUTER_API_KEY or OPENAI_API_KEY, plus SESSION_SECRET

# 3. Fetch deps and build
go mod tidy
go build ./...

# 4. Run
go run ./cmd/server
# → http://localhost:8080
```

The first run creates `./diet-app.db` and runs `migrations/0001_init.sql`.

## Config

See `.env.example` for the full list. Important keys:

- `SESSION_SECRET` — random 32+ bytes; used to sign session cookies.
- `DEFAULT_AI_SOURCE` — `openai` | `openrouter` | `ollama` | `lmstudio`.
- `DEFAULT_AI_MODEL` — model id sent to that provider.
- `ALLOW_ANONYMOUS` — when `true`, every visitor gets a throwaway user
  (matches the original Supabase anonymous fallback).

Each user can override `source` and `model_id` on `/profile`.

## AI provider notes

- **OpenRouter** is the default. Free models rotate in/out of rate limits, so
  the app expects 429s and shows a clear error. (Fallback chain — the
  TypeScript app's `OPENROUTER_FALLBACKS` — is not yet ported; it's a
  follow-up.)
- **OpenAI** requires `OPENAI_API_KEY` and a paid account.
- **Ollama** runs locally; set `OLLAMA_BASE_URL` if it's not on the default
  port.
- **LM Studio** is the same with a different default port.
- **Anthropic** is not in MVP — add later by introducing a second client
  behind a small interface (no API change needed).

## What's not in MVP (deliberately)

These were left out so the slice is small. Add them in follow-ups:

- Streaming chat (SSE). The agent runs a non-streaming `Chat` request; the UI
  shows the full reply when it arrives. Switching to SSE is a single handler
  change.
- Anthropic provider.
- OpenRouter free-model fallback chain.
- Stripe tier gating (subscription page, checkout, portal, webhook).
- Meal plans (`/plan`).
- Grocery lists (`/groceries`).
- Onboarding flow.
- Server-side rate limiting per IP for anonymous users (current limiter keys
  on user id only).
- Image generation / caching, etc.

## Migration from the old Next.js app

The Go app uses its own SQLite file — there is no automatic importer from the
old Supabase Postgres DB. If you want to move data over, write a one-off
script that queries the old `recipes`, `pantry`, `restrictions`, `blacklist`
tables and inserts into the matching SQLite tables. Schemas are intentionally
similar so this should be straightforward.

## License

Same as the original project.
