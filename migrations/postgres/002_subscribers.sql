create table if not exists subscribers (
    id bigserial primary key,
    msisdn text not null unique,
    enabled boolean default true,
    adaptation_class text default 'default',
    max_msg_size bigint default 307200,
    home_mmsc text,
    created_at timestamptz default now(),
    updated_at timestamptz default now()
);

create table if not exists adaptation_classes (
    name text primary key,
    max_msg_size_bytes bigint default 307200,
    max_image_width int default 640,
    max_image_height int default 480,
    allowed_img_types text[] default array['image/jpeg','image/gif','image/png'],
    allowed_audio_types text[] default array['audio/amr','audio/aac','audio/mp3'],
    allowed_video_types text[] default array['video/mp4','video/3gpp'],
    max_video_secs int default 60,
    max_audio_secs int default 120,
    created_at timestamptz default now(),
    updated_at timestamptz default now()
);

insert into adaptation_classes (name)
values ('default')
on conflict (name) do nothing;
