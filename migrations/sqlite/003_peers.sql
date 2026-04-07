create table if not exists mm4_peers (
    id integer primary key autoincrement,
    domain text not null unique,
    smtp_host text not null,
    smtp_port integer not null default 25,
    smtp_auth integer default 0,
    smtp_user text,
    smtp_pass text,
    tls_enabled integer default 1,
    allowed_ips text,
    active integer default 1,
    created_at text default current_timestamp,
    updated_at text default current_timestamp
);
