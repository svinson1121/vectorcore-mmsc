alter table smpp_upstream add column if not exists dest_addr_ton int not null default 1;
alter table smpp_upstream add column if not exists dest_addr_npi int not null default 1;
