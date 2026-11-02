-- Action-layer tables: calorie targets and persistent grocery lists.
-- SQLite has no ADD COLUMN IF NOT EXISTS, so targets live in their own table.
create table if not exists profile_targets (
  user_id text primary key references users(id) on delete cascade,
  calorie_target integer,
  updated_at text not null default (datetime('now'))
);

create table if not exists grocery_lists (
  user_id text not null references users(id) on delete cascade,
  week_start text not null,
  items_json text not null default '[]',
  updated_at text not null default (datetime('now')),
  primary key (user_id, week_start)
);
create index if not exists grocery_lists_user_idx on grocery_lists(user_id, week_start);
