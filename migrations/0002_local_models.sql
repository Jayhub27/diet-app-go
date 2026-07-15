-- Local AI model catalog. Server-wide; not per-user.
-- Lists GGUF models that have been downloaded into the models directory,
-- optionally paired with an mmproj projector for vision-language models.

create table if not exists local_models (
  id          text primary key,
  name        text not null,
  model_path  text not null,
  mmproj_path text,
  source_url  text,
  is_vision   integer not null default 0,
  size_bytes  integer not null default 0,
  created_at  text not null default (datetime('now'))
);

create index if not exists local_models_name_idx on local_models(name);
