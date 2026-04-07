create table if not exists adaptation_classes (
    name text primary key,
    max_msg_size_bytes bigint not null default 307200,
    max_image_width int not null default 640,
    max_image_height int not null default 480,
    allowed_img_types text[] not null default array['image/jpeg','image/gif','image/png'],
    allowed_audio_types text[] not null default array['audio/amr','audio/mpeg','audio/mp4'],
    allowed_video_types text[] not null default array['video/3gpp','video/mp4'],
    created_at timestamptz default now(),
    updated_at timestamptz default now()
);

insert into adaptation_classes (
    name, max_msg_size_bytes, max_image_width, max_image_height
) values (
    'default', 307200, 640, 480
)
on conflict (name) do nothing;

