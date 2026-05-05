alter table smpp_upstream add column dest_addr_ton integer not null default 1;
alter table smpp_upstream add column dest_addr_npi integer not null default 1;
