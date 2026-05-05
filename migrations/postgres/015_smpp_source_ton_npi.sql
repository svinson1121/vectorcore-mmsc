alter table smpp_upstream add column if not exists source_addr_ton int null;
alter table smpp_upstream add column if not exists source_addr_npi int null;
