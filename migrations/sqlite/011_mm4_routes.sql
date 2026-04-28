alter table mm4_peers add column name text not null default '';

update mm4_peers set name = domain where name = '';

create table if not exists mm4_routes (
    id integer primary key autoincrement,
    name text not null,
    match_type text not null,
    match_value text not null,
    egress_peer_domain text not null references mm4_peers(domain) on update cascade on delete restrict,
    priority integer not null default 100,
    active integer default 1,
    created_at text default current_timestamp,
    updated_at text default current_timestamp,
    check (match_type in ('msisdn_exact', 'msisdn_prefix', 'recipient_domain'))
);

create index if not exists mm4_routes_active_match_idx on mm4_routes(active, match_type, match_value);
create index if not exists mm4_routes_peer_idx on mm4_routes(egress_peer_domain);
