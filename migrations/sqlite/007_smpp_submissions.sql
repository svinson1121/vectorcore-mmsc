create table if not exists smpp_submissions (
    upstream_name text not null,
    smpp_message_id text not null,
    internal_message_id text not null references messages(id) on delete cascade,
    recipient text not null,
    segment_index integer not null default 0,
    segment_count integer not null default 1,
    state integer not null default 0,
    error_text text,
    submitted_at text not null default current_timestamp,
    completed_at text,
    primary key (upstream_name, smpp_message_id)
);

create index if not exists idx_smpp_submissions_internal_message
    on smpp_submissions (internal_message_id);
