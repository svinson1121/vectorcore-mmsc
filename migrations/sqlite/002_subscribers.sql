create table if not exists subscribers (
    id integer primary key autoincrement,
    msisdn text not null unique,
    enabled integer default 1,
    adaptation_class text default 'default',
    max_msg_size integer default 307200,
    home_mmsc text,
    created_at text default current_timestamp,
    updated_at text default current_timestamp
);

create table if not exists adaptation_classes (
    name text primary key,
    max_msg_size_bytes integer default 307200,
    max_image_width integer default 640,
    max_image_height integer default 480,
    allowed_img_types text default 'image/jpeg,image/gif,image/png',
    allowed_audio_types text default 'audio/amr,audio/aac,audio/mp3',
    allowed_video_types text default 'video/mp4,video/3gpp',
    max_video_secs integer default 60,
    max_audio_secs integer default 120,
    created_at text default current_timestamp,
    updated_at text default current_timestamp
);

insert or ignore into adaptation_classes (name) values ('default');
