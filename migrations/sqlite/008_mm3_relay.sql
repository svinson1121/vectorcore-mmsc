create table if not exists mm3_relay (
    id integer primary key check (id = 1),
    enabled integer not null default 0,
    smtp_host text not null default '',
    smtp_port integer not null default 25,
    smtp_auth integer not null default 0,
    smtp_user text,
    smtp_pass text,
    tls_enabled integer not null default 0,
    default_sender_domain text not null default '',
    default_from_address text,
    created_at text default current_timestamp,
    updated_at text default current_timestamp
);
