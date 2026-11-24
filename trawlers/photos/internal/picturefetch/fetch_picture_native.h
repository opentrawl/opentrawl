#ifndef OPENTRAWL_FETCH_PICTURE_NATIVE_H
#define OPENTRAWL_FETCH_PICTURE_NATIVE_H

#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

typedef enum {
  NATIVE_PICTURE_FETCH_OUTCOME_FETCHED = 0,
  NATIVE_PICTURE_FETCH_OUTCOME_FAILED_TRY_AGAIN_LATER = 1,
  NATIVE_PICTURE_FETCH_OUTCOME_FAILED_FOR_GOOD = 2,
  NATIVE_PICTURE_FETCH_OUTCOME_APPLE_PHOTOS_ACCESS_NOT_GRANTED = 3,
} NativePictureFetchOutcome;

typedef struct {
  NativePictureFetchOutcome picture_fetch_outcome;
  char *picture_fetch_failure_message;
  uint8_t *picture_jpeg_bytes;
  size_t picture_jpeg_byte_count;
  bool camera_bearing_is_recorded;
  double camera_bearing_degrees;
  // The GPS ImgDirectionRef letter: 'T' for true north, 'M' for magnetic.
  char camera_bearing_reference_letter;
} NativeFetchedPicture;

NativeFetchedPicture fetchUprightPictureFromFile(const char *picture_file_path,
                                                 bool picture_file_is_video,
                                                 int32_t picture_long_edge_pixel_limit);

NativeFetchedPicture fetchUprightPictureFromApplePhotos(const char *apple_photo_uuid,
                                                        int32_t picture_long_edge_pixel_limit);

void freeNativeFetchedPicture(NativeFetchedPicture fetched_picture);

#endif
