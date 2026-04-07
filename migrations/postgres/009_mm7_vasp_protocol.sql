alter table mm7_vasps
	add column if not exists protocol text not null default 'soap',
	add column if not exists version text;

update mm7_vasps
set protocol = 'soap'
where protocol is null or protocol = '';
