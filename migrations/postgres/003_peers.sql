create table if not exists mm4_peers (
    id bigserial primary key,
    domain text not null unique,
    smtp_host text not null,
    smtp_port int not null default 25,
    smtp_auth boolean default false,
    smtp_user text,
    smtp_pass text,
    tls_enabled boolean default true,
    allowed_ips inet[],
    active boolean default true,
    created_at timestamptz default now(),
    updated_at timestamptz default now()
);
