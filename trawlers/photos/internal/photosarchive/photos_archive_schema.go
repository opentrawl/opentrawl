package photosarchive

// The archive holds three families of tables, named by where their data came
// from: apple_library_* is what Apple's Photos library holds, picture_* is
// what the picture file holds, and the rest is derived. Enum columns hold the
// protobuf enum value name. Times are UTC RFC 3339 text.
const photosArchiveSchema = `
create table if not exists apple_library_photo (
  apple_photo_uuid                          text primary key,
  apple_library_scope                       text not null,
  apple_shared_library_contributor_is_owner integer,
  apple_media_kind                          integer not null,
  apple_media_subtype                       integer not null,
  is_apple_detected_screenshot              integer not null,
  capture_time_utc                          text not null,
  capture_utc_offset_seconds                integer,
  capture_time_zone_name                    text,
  capture_time_zone_source                  text not null,
  apple_device_utc_offset_seconds           integer,
  apple_device_time_zone_name               text,
  apple_device_utc_offset_source            text not null,
  camera_latitude                           real,
  camera_longitude                          real,
  camera_altitude_metres                    real,
  camera_horizontal_accuracy_metres         real,
  apple_china_shifted_latitude              real,
  apple_china_shifted_longitude             real,
  apple_stored_full_address                 text,
  apple_stored_iso_country_code             text,
  apple_stored_country_name                 text,
  apple_stored_state_name                   text,
  apple_stored_county_name                  text,
  apple_stored_town_name                    text,
  apple_stored_neighbourhood_name           text,
  apple_stored_street                       text,
  apple_stored_point_of_interest_name       text,
  apple_stored_water_name                   text,
  apple_stored_island_name                  text,
  apple_says_home                           integer,
  apple_moment_uuid                         text,
  apple_moment_title                        text,
  camera_make                               text,
  camera_model                              text,
  lens_model                                text,
  focal_length_millimetres                  real,
  focal_length_35mm_equivalent_millimetres  integer,
  aperture_f_number                         real,
  exposure_time_seconds                     real,
  iso_speed                                 integer,
  pixel_width                               integer not null,
  pixel_height                              integer not null,
  apple_duration_seconds                    real,
  apple_burst_uuid                          text,
  apple_caption                             text,
  apple_title                               text,
  is_favourite                              integer not null,
  is_hidden                                 integer not null,
  apple_adjustments_state                   integer not null,
  apple_modification_time_utc               text,
  apple_library_photo_fingerprint           text not null,
  first_seen_time_utc                       text not null,
  absent_from_apple_library_since_time_utc  text
);
create index if not exists apple_library_photo_by_capture_time on apple_library_photo(capture_time_utc desc);

create table if not exists apple_library_photo_place_area (
  apple_photo_uuid          text not null,
  area_rank_small_to_large  integer not null,
  area_name                 text not null,
  apple_area_square_metres  real,
  apple_place_type          integer not null,
  primary key (apple_photo_uuid, area_rank_small_to_large)
);

create table if not exists apple_library_photo_named_person (
  apple_photo_uuid   text not null,
  apple_person_uuid  text not null,
  person_full_name   text not null,
  primary key (apple_photo_uuid, apple_person_uuid)
);

create table if not exists apple_library_photo_album (
  apple_photo_uuid  text not null,
  apple_album_uuid  text not null,
  album_title       text not null,
  primary key (apple_photo_uuid, apple_album_uuid)
);

create table if not exists apple_library_photo_recognised_text_line (
  apple_photo_uuid  text not null,
  line_rank         integer not null,
  recognised_text   text not null,
  primary key (apple_photo_uuid, line_rank)
);

create table if not exists apple_library_trip (
  apple_trip_uuid            text primary key,
  apple_trip_place_title     text,
  apple_trip_date_title      text,
  apple_trip_start_time_utc  text not null,
  apple_trip_end_time_utc    text not null
);

create table if not exists apple_library_photo_trip (
  apple_photo_uuid  text not null,
  apple_trip_uuid   text not null,
  primary key (apple_photo_uuid, apple_trip_uuid)
);

create table if not exists picture_fetch (
  apple_photo_uuid                         text primary key,
  fetched_for_apple_modification_time_utc  text not null,
  picture_fetch_outcome                    text not null,
  picture_fetch_failure_message            text,
  retry_not_before_time_utc                text,
  picture_sha256                           text,
  picture_byte_count                       integer,
  camera_bearing_degrees                   real,
  camera_bearing_reference                 text,
  fetched_time_utc                         text not null
);

create table if not exists picture_file_metadata_field (
  apple_photo_uuid           text not null,
  metadata_group             text not null,
  metadata_field_name        text not null,
  metadata_field_value_text  text not null
);

create table if not exists place_lookup (
  place_lookup_request_sha256   text primary key,
  place_provider                text not null,
  camera_spot_key               text not null,
  place_lookup_request_text     text not null,
  place_lookup_outcome          text not null,
  place_lookup_failure_message  text,
  retry_not_before_time_utc     text,
  provider_response_body        blob,
  looked_up_time_utc            text not null
);
create index if not exists place_lookup_by_camera_spot on place_lookup(camera_spot_key, place_provider);

create table if not exists nearby_place (
  place_lookup_request_sha256  text not null,
  place_rank                   integer not null,
  place_name                   text not null,
  place_kind                   text not null,
  place_latitude               real not null,
  place_longitude              real not null,
  primary key (place_lookup_request_sha256, place_rank)
);

create table if not exists photo_card_model_input (
  apple_photo_uuid               text primary key,
  photo_card_model_input_text    text not null,
  photo_card_model_input_sha256  text not null,
  written_time_utc               text not null
);

create table if not exists photo_card_model_input_section_used (
  apple_photo_uuid  text not null,
  section_name      text not null,
  section_sha256    text not null,
  primary key (apple_photo_uuid, section_name)
);

create table if not exists photo_card (
  apple_photo_uuid                    text primary key,
  photo_card_outcome                  text not null,
  photo_card_model_input_text_used    text not null,
  photo_card_model_input_sha256_used  text not null,
  picture_sha256_used                 text not null,
  card_model_name                     text not null,
  one_line_summary                    text,
  description                         text,
  place_shown_judgement               text,
  place_shown_name                    text,
  place_shown_reasoning               text,
  raw_model_answer                    text,
  input_tokens                        integer,
  output_tokens                       integer,
  reasoning_tokens                    integer,
  retry_not_before_time_utc           text,
  written_time_utc                    text not null
);

create table if not exists photo_card_visible_text (
  apple_photo_uuid    text not null,
  text_group_rank     integer not null,
  where_text_appears  text not null,
  text_as_written     text not null,
  english_rendering   text,
  primary key (apple_photo_uuid, text_group_rank)
);

-- One row per step: when it last ran and why it stopped. Work is never read
-- from here; a step's work list comes from the tables above.
create table if not exists photos_step_last_run (
  photos_step_name                   text primary key,
  last_run_started_time_utc          text not null,
  last_run_finished_time_utc         text not null,
  last_run_item_count                integer not null,
  last_run_stop_reason               text not null
);

-- Search rows for Apple's facts. Runs of Chinese, Japanese and Korean
-- characters are indexed as overlapping two-character pieces.
create virtual table if not exists apple_library_photo_search using fts5(
  apple_photo_uuid unindexed,
  apple_caption_and_title,
  apple_place_names,
  apple_named_people,
  apple_album_titles,
  apple_moment_title,
  apple_recognised_text
);
`
