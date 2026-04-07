create table if not exists mm3_relay (
    id smallint primary key default 1 check (id = 1),
    enabled boolean not null default false,
    smtp_host text not null default '',
    smtp_port int not null default 25,
    smtp_auth boolean not null default false,
    smtp_user text,
    smtp_pass text,
    tls_enabled boolean not null default false,
    default_sender_domain text not null default '',
    default_from_address text,
    created_at timestamptz default now(),
    updated_at timestamptz default now()
);

drop trigger if exists mm3_relay_change on mm3_relay;
create trigger mm3_relay_change after insert or update or delete on mm3_relay
    for each row execute function notify_config_change();
