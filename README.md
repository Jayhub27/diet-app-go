# DietApp Lite (Go + HTMX + SQLite)

DietApp Lite is the lightweight Android app and its self-hosted server. The
full DietApp is the Next.js app in the browser and on Android. Lite covers the
essentials locally: meal planning from what's already in the kitchen,
real-time label and barcode checks, and real products checked against your
restrictions. One Go binary, SQLite, HTMX, and an OpenAI-compatible agent
(OpenAI, OpenRouter, Ollama, LM Studio).

## Goals

- One binary, one SQLite file, zero JS build step.
- Same core slice as the web app: streaming chat, pantry, grocery list, recipe
  library, profile (restrictions, blacklist, calorie target, AI settings),
  product/barcode checks, recipe import from a URL, chat history + delete, web
  search (Exa/Parallel), email+password auth (with anonymous fallback).
- The agent acts on the app instead of only advising: it can add or remove
  blacklist items and restrictions, set the calorie target, edit the pantry and
  grocery list, and open the matching screen.

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
migrations/                   # idempotent SQL schema + action-layer tables
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

The first run creates `./diet-app.db` and applies every SQL file in
`migrations/` in order.

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

- Anthropic provider.
- OpenRouter free-model fallback chain (the app now defaults to
  `nvidia/nemotron-3.5-super-vl:free`).
- Stripe tier gating (subscription page, checkout, portal, webhook).
- Meal plans (`/plan`). The grocery list itself is implemented at `/groceries`.
- Onboarding flow.
- Server-side rate limiting per IP for anonymous users (current limiter keys
  on user id only).
- Credit purchase / subscription management (Stripe).
- Image generation / caching, etc.

## Migration from the old Next.js app

The Go app uses its own SQLite file — there is no automatic importer from the
old Supabase Postgres DB. If you want to move data over, write a one-off
script that queries the old `recipes`, `pantry`, `restrictions`, `blacklist`
tables and inserts into the matching SQLite tables. Schemas are intentionally
similar so this should be straightforward.

## License

Same as the original project.
