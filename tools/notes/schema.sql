create table if not exists notes_items (
  id integer primary key autoincrement,
  text text not null,
  session text,
  created_at text not null default (datetime('now'))
);
