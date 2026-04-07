create table if not exists mm7_vasps (
    id bigserial primary key,
    vasp_id text not null unique,
    vas_id text,
    shared_secret text,
    allowed_ips inet[],
    deliver_url text,
    report_url text,
    max_msg_size bigint default 1048576,
    active boolean default true,
    created_at timestamptz default now(),
    updated_at timestamptz default now()
);
