create table if not exists message_events (
    id integer primary key autoincrement,
    message_id text not null references messages(id) on delete cascade,
    source text not null,
    event_type text not null,
    summary text not null,
    detail text,
    created_at text not null default current_timestamp
);

create index if not exists idx_message_events_message_id_created
    on message_events (message_id, created_at desc, id desc);
