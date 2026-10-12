#ifndef OPENTRAWL_APPLE_CHARACTER_RECOGNITION_NATIVE_H
#define OPENTRAWL_APPLE_CHARACTER_RECOGNITION_NATIVE_H

#include <stddef.h>
#include <stdint.h>

typedef struct {
  uint8_t *utf8_bytes;
  size_t utf8_byte_count;
} AppleRecognisedTextLine;

typedef struct {
  AppleRecognisedTextLine *recognised_text_lines;
  size_t recognised_text_line_count;
  char *failure_message;
} AppleCharacterRecognitionDecodeResult;

AppleCharacterRecognitionDecodeResult decodeAppleCharacterRecognitionRegionTree(
    const uint8_t *character_recognition_archive_bytes,
    size_t character_recognition_archive_byte_count);

void freeAppleCharacterRecognitionRegionTree(
    AppleCharacterRecognitionDecodeResult recognition);

#endif
