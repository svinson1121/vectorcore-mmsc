create table if not exists mm7_vasps (
    id integer primary key autoincrement,
    vasp_id text not null unique,
    vas_id text,
    shared_secret text,
    allowed_ips text,
    deliver_url text,
    report_url text,
    max_msg_size integer default 1048576,
    active integer default 1,
    created_at text default current_timestamp,
    updated_at text default current_timestamp
);
