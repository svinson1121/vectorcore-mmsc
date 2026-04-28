create table if not exists routing_rules (
    id bigserial primary key,
    name text not null,
    match_type text not null,
    match_value text not null,
    egress_type text not null,
    egress_target text,
    priority int not null default 100,
    active boolean default true,
    created_at timestamptz default now(),
    updated_at timestamptz default now(),
    constraint routing_rules_match_type_check check (match_type in ('msisdn_exact', 'msisdn_prefix', 'recipient_domain')),
    constraint routing_rules_egress_type_check check (egress_type in ('local', 'reject', 'mm3', 'mm4'))
);

insert into routing_rules (name, match_type, match_value, egress_type, egress_target, priority, active)
select name, match_type, match_value, 'mm4', egress_peer_domain, priority, active
from mm4_routes
where not exists (
    select 1 from routing_rules r
    where r.match_type = mm4_routes.match_type
      and r.match_value = mm4_routes.match_value
      and r.egress_type = 'mm4'
      and r.egress_target = mm4_routes.egress_peer_domain
);

create index if not exists routing_rules_active_match_idx on routing_rules(active, match_type, match_value);
create index if not exists routing_rules_egress_idx on routing_rules(egress_type, egress_target);
