//go:build darwin

#import <Foundation/Foundation.h>
#import <Vision/Vision.h>
#include <dispatch/dispatch.h>
#include <dlfcn.h>
#include <stdlib.h>
#include <string.h>

#include "apple_character_recognition_native.h"

static void *appleCoreRecognitionFramework;
static char *appleCoreRecognitionFrameworkFailure;
static dispatch_once_t appleCoreRecognitionFrameworkLoad;

static void loadAppleCoreRecognitionFramework(void *context) {
  (void)context;
  appleCoreRecognitionFramework = dlopen(
      "/System/Library/PrivateFrameworks/CoreRecognition.framework/CoreRecognition",
      RTLD_NOW);
  if (appleCoreRecognitionFramework == NULL) {
    appleCoreRecognitionFrameworkFailure = strdup(dlerror());
  }
}

static void appendAppleRecognisedTextLinesFromChildren(
    NSObject *region, Class lineRegionClass,
    NSMutableArray<NSString *> *recognisedTextLines) {
  if ([region isKindOfClass:lineRegionClass]) {
    NSString *lineText = [region valueForKey:@"text"];
    if (![lineText isKindOfClass:[NSString class]]) {
      [NSException raise:NSInvalidUnarchiveOperationException
                  format:@"Apple recognised line has no text"];
    }
    [recognisedTextLines addObject:lineText];
  }
  // Candidate readings and table references are separate from the source
  // hierarchy. Only children define the recognised lines and their order.
  for (NSObject *child in [region valueForKey:@"children"]) {
    appendAppleRecognisedTextLinesFromChildren(child, lineRegionClass,
                                               recognisedTextLines);
  }
}

AppleCharacterRecognitionDecodeResult decodeAppleCharacterRecognitionRegionTree(
    const uint8_t *characterRecognitionArchiveBytes,
    size_t characterRecognitionArchiveByteCount) {
  AppleCharacterRecognitionDecodeResult recognition = {0};
  dispatch_once_f(&appleCoreRecognitionFrameworkLoad, NULL,
                  loadAppleCoreRecognitionFramework);
  if (appleCoreRecognitionFramework == NULL) {
    recognition.failure_message = strdup(appleCoreRecognitionFrameworkFailure);
    return recognition;
  }
  @autoreleasepool {
    NSKeyedUnarchiver *decoder = nil;
    @try {
      // Referencing Vision loads its archived observation classes.
      [VNObservation class];
      NSError *decodeError = nil;
      NSData *archive = [NSData dataWithBytes:characterRecognitionArchiveBytes
                                      length:characterRecognitionArchiveByteCount];
      decoder = [[NSKeyedUnarchiver alloc] initForReadingFromData:archive
                                                          error:&decodeError];
      if (decoder == nil) {
        recognition.failure_message = strdup(decodeError.localizedDescription.UTF8String);
        return recognition;
      }
      decoder.requiresSecureCoding = NO;
      NSObject *observation = [decoder decodeObjectForKey:NSKeyedArchiveRootObjectKey];
      NSObject *topLevelRegion = [observation valueForKey:@"topLevelRegion"];
      Class lineRegionClass = NSClassFromString(@"CRLineOutputRegion");
      if (topLevelRegion == nil || lineRegionClass == Nil) {
        [NSException raise:NSInvalidUnarchiveOperationException
                    format:@"Apple recognised text archive has no region tree"];
      }
      NSMutableArray<NSString *> *recognisedTextLines = [NSMutableArray array];
      appendAppleRecognisedTextLinesFromChildren(topLevelRegion, lineRegionClass,
                                                 recognisedTextLines);
      [decoder finishDecoding];
      if (decoder.error != nil) {
        recognition.failure_message = strdup(decoder.error.localizedDescription.UTF8String);
        return recognition;
      }
      recognition.recognised_text_line_count = recognisedTextLines.count;
      recognition.recognised_text_lines = calloc(
          recognition.recognised_text_line_count, sizeof(AppleRecognisedTextLine));
      for (NSUInteger lineIndex = 0; lineIndex < recognisedTextLines.count; lineIndex++) {
        NSData *lineBytes = [recognisedTextLines[lineIndex]
            dataUsingEncoding:NSUTF8StringEncoding];
        AppleRecognisedTextLine *line = &recognition.recognised_text_lines[lineIndex];
        line->utf8_byte_count = lineBytes.length;
        if (lineBytes.length > 0) {
          line->utf8_bytes = malloc(lineBytes.length);
          memcpy(line->utf8_bytes, lineBytes.bytes, lineBytes.length);
        }
      }
    } @catch (NSException *exception) {
      recognition.failure_message = strdup(exception.reason.UTF8String);
    } @finally {
      [decoder release];
    }
  }
  return recognition;
}

void freeAppleCharacterRecognitionRegionTree(
    AppleCharacterRecognitionDecodeResult recognition) {
  for (size_t lineIndex = 0; lineIndex < recognition.recognised_text_line_count;
       lineIndex++) {
    free(recognition.recognised_text_lines[lineIndex].utf8_bytes);
  }
  free(recognition.recognised_text_lines);
  free(recognition.failure_message);
}
