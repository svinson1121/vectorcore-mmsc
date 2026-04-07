create table if not exists smpp_upstream (
    id bigserial primary key,
    name text not null unique,
    host text not null,
    port int not null default 2775,
    system_id text not null,
    password text not null,
    system_type text default '',
    bind_mode text default 'transceiver',
    enquire_link int default 30,
    reconnect_wait int default 5,
    active boolean default true,
    created_at timestamptz default now(),
    updated_at timestamptz default now()
);

create or replace function notify_config_change()
returns trigger as $$
declare
    payload text;
begin
    payload := tg_table_name;
    perform pg_notify('config_changed', payload);
    return coalesce(new, old);
end;
$$ language plpgsql;

drop trigger if exists smpp_upstream_change on smpp_upstream;
create trigger smpp_upstream_change after insert or update or delete on smpp_upstream
    for each row execute function notify_config_change();

drop trigger if exists mm4_peers_change on mm4_peers;
create trigger mm4_peers_change after insert or update or delete on mm4_peers
    for each row execute function notify_config_change();

drop trigger if exists mm7_vasps_change on mm7_vasps;
create trigger mm7_vasps_change after insert or update or delete on mm7_vasps
    for each row execute function notify_config_change();

drop trigger if exists adaptation_classes_change on adaptation_classes;
create trigger adaptation_classes_change after insert or update or delete on adaptation_classes
    for each row execute function notify_config_change();
