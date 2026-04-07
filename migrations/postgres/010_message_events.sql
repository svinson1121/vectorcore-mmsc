create table if not exists message_events (
    id bigserial primary key,
    message_id text not null references messages(id) on delete cascade,
    source text not null,
    event_type text not null,
    summary text not null,
    detail text,
    created_at timestamptz not null default now()
);

create index if not exists idx_message_events_message_id_created
    on message_events (message_id, created_at desc, id desc);
