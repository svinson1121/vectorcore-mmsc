alter table mm4_peers add column if not exists name text;

update mm4_peers set name = domain where name is null or name = '';

alter table mm4_peers alter column name set not null;

create table if not exists mm4_routes (
    id bigserial primary key,
    name text not null,
    match_type text not null,
    match_value text not null,
    egress_peer_domain text not null references mm4_peers(domain) on update cascade on delete restrict,
    priority int not null default 100,
    active boolean default true,
    created_at timestamptz default now(),
    updated_at timestamptz default now(),
    constraint mm4_routes_match_type_check check (match_type in ('msisdn_exact', 'msisdn_prefix', 'recipient_domain'))
);

create index if not exists mm4_routes_active_match_idx on mm4_routes(active, match_type, match_value);
create index if not exists mm4_routes_peer_idx on mm4_routes(egress_peer_domain);
