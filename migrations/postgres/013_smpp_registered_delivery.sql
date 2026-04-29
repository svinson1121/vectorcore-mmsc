alter table smpp_upstream add column if not exists registered_delivery int not null default 0;
