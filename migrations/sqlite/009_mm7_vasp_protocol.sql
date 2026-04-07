alter table mm7_vasps add column protocol text not null default 'soap';
alter table mm7_vasps add column version text;

update mm7_vasps
set protocol = 'soap'
where protocol is null or protocol = '';
