-- DietApp SQLite schema
-- Mirrors the original Supabase Postgres schema, minus auth.users (replaced
-- by a local users table) and minus the usage_log (token metering). Safe to
-- re-run via the migration runner (uses IF NOT EXISTS).

create table if not exists users (
  id text primary key,
  email text not null unique,
  password_hash text not null,
  is_anonymous integer not null default 0,
  subscription_tier text not null default 'free',
  created_at text not null default (datetime('now')),
  updated_at text not null default (datetime('now'))
);

create table if not exists sessions (
  id text primary key,
  user_id text not null references users(id) on delete cascade,
  expires_at text not null,
  created_at text not null default (datetime('now'))
);
create index if not exists sessions_user_idx on sessions(user_id);

create table if not exists user_settings (
  user_id text primary key references users(id) on delete cascade,
  source text not null default 'openrouter',
  model_id text not null default 'meta-llama/llama-3.3-70b-instruct:free',
  base_url text,
  api_key text,
  updated_at text not null default (datetime('now'))
);

create table if not exists recipes (
  id text primary key,
  user_id text not null references users(id) on delete cascade,
  title text not null,
  description text,
  servings integer not null default 1,
  prep_minutes integer not null default 0,
  cook_minutes integer not null default 0,
  ingredients_json text not null,
  steps_json text not null,
  dietary_tags_json text not null,
  nutrition_json text not null,
  source_prompt text,
  created_at text not null default (datetime('now'))
);
create index if not exists recipes_user_idx on recipes(user_id, created_at desc);

create table if not exists pantry (
  id text primary key,
  user_id text not null references users(id) on delete cascade,
  name text not null,
  amount real,
  unit text,
  created_at text not null default (datetime('now'))
);
create index if not exists pantry_user_idx on pantry(user_id);

create table if not exists restrictions (
  id text primary key,
  user_id text not null references users(id) on delete cascade,
  restriction text not null,
  severity text not null default 'avoid' check (severity in ('avoid','intolerance','anaphylaxis')),
  created_at text not null default (datetime('now')),
  unique (user_id, restriction)
);

create table if not exists blacklist (
  id text primary key,
  user_id text not null references users(id) on delete cascade,
  ingredient text not null,
  created_at text not null default (datetime('now')),
  unique (user_id, ingredient)
);

create table if not exists rate_limit_events (
  bucket text not null,
  at_ms integer not null
);
create index if not exists rate_limit_bucket_idx on rate_limit_events(bucket, at_ms);

create table if not exists conversations (
  id text primary key,
  user_id text not null references users(id) on delete cascade,
  title text not null default 'New chat',
  created_at text not null default (datetime('now')),
  updated_at text not null default (datetime('now'))
);
create index if not exists conversations_user_idx on conversations(user_id, updated_at desc);

create table if not exists messages (
  id text primary key,
  conversation_id text not null references conversations(id) on delete cascade,
  user_id text not null references users(id) on delete cascade,
  role text not null check (role in ('user','assistant','tool','system')),
  content text not null,
  tool_name text,
  created_at text not null default (datetime('now'))
);
create index if not exists messages_conv_idx on messages(conversation_id, created_at);
