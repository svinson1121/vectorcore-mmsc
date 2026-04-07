create table if not exists smpp_upstream (
    id integer primary key autoincrement,
    name text not null unique,
    host text not null,
    port integer not null default 2775,
    system_id text not null,
    password text not null,
    system_type text default '',
    bind_mode text default 'transceiver',
    enquire_link integer default 30,
    reconnect_wait integer default 5,
    active integer default 1,
    created_at text default current_timestamp,
    updated_at text default current_timestamp
);
