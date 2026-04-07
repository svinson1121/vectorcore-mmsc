create table if not exists adaptation_classes (
    name text primary key,
    max_msg_size_bytes integer not null default 307200,
    max_image_width integer not null default 640,
    max_image_height integer not null default 480,
    allowed_img_types text not null default 'image/jpeg,image/gif,image/png',
    allowed_audio_types text not null default 'audio/amr,audio/mpeg,audio/mp4',
    allowed_video_types text not null default 'video/3gpp,video/mp4',
    created_at text default current_timestamp,
    updated_at text default current_timestamp
);

insert or ignore into adaptation_classes (
    name, max_msg_size_bytes, max_image_width, max_image_height
) values (
    'default', 307200, 640, 480
);
