#import <Foundation/Foundation.h>
#import <Photos/Photos.h>
#import <ImageIO/ImageIO.h>
#import <dispatch/dispatch.h>
#include <math.h>
#include <stdlib.h>
#include <string.h>

static void csError(char **out, NSString *message) {
  if (out == NULL) return;
  NSData *data = [message dataUsingEncoding:NSUTF8StringEncoding];
  char *value = malloc(data.length + 1);
  if (value == NULL) return;
  memcpy(value, data.bytes, data.length); value[data.length] = '\0'; *out = value;
}

static void csStage(char **out, NSString *stage) {
  csError(out, stage);
}

static NSString *csUUID(NSString *identifier) {
  return [[identifier componentsSeparatedByString:@"/"] firstObject].lowercaseString;
}

static PHAuthorizationStatus csStatus(void) {
  if (@available(macOS 11.0, *)) return [PHPhotoLibrary authorizationStatusForAccessLevel:PHAccessLevelReadWrite];
  return [PHPhotoLibrary authorizationStatus];
}

int photoscrawl_export_current_still_matching(const char *assetUUID, long long modificationUnixSeconds, int modificationMicroseconds, const char *destinationPath, int allowNetwork, long long timeoutMilliseconds, char **mediaTypeOut, long long *orientationOut, long long *pixelWidthOut, long long *pixelHeightOut, char **errorOut, char **errorDomainOut, long long *errorCodeOut, int *callbackCancelledOut, int *callbackDegradedOut, int *callbackInCloudOut, int *callbackReturnedOut, char **stageOut) {
  @autoreleasepool {
    if (mediaTypeOut) *mediaTypeOut = NULL;
    if (errorOut) *errorOut = NULL;
    if (errorDomainOut) *errorDomainOut = NULL;
    if (errorCodeOut) *errorCodeOut = 0;
    if (callbackCancelledOut) *callbackCancelledOut = 0;
    if (callbackDegradedOut) *callbackDegradedOut = 0;
    if (callbackInCloudOut) *callbackInCloudOut = 0;
    if (callbackReturnedOut) *callbackReturnedOut = 0;
    if (stageOut) *stageOut = NULL;
    NSString *uuid = assetUUID ? [NSString stringWithUTF8String:assetUUID] : @"";
    NSString *path = destinationPath ? [NSString stringWithUTF8String:destinationPath] : @"";
    if (uuid.length == 0 || modificationUnixSeconds <= 0 || modificationMicroseconds < 0 || modificationMicroseconds >= 1000000 || path.length == 0) { csError(errorOut, @"asset UUID, canonical modification instant and destination path are required"); return 0; }
    PHAuthorizationStatus status = csStatus();
    if (status != PHAuthorizationStatusAuthorized && status != PHAuthorizationStatusLimited) { csError(errorOut, @"photos_access:denied"); return 0; }
    PHFetchResult<PHAsset *> *assets = [PHAsset fetchAssetsWithOptions:nil];
    PHAsset *asset = nil;
    for (PHAsset *candidate in assets) { if ([csUUID(candidate.localIdentifier) isEqualToString:uuid.lowercaseString]) { asset = candidate; break; } }
    if (asset == nil) { csError(errorOut, @"photokit asset not found"); return 0; }
    if (asset.modificationDate == nil) { csStage(stageOut, @"selection_validation"); csError(errorOut, @"selected asset modification instant does not match PhotoKit"); return 0; }
    NSTimeInterval observed = asset.modificationDate.timeIntervalSince1970;
    long long observedSeconds = (long long)floor(observed);
    long long observedMicroseconds = llround((observed - observedSeconds) * 1000000.0);
    if (observedMicroseconds == 1000000) { observedSeconds++; observedMicroseconds = 0; }
    if (observedSeconds != modificationUnixSeconds || observedMicroseconds != modificationMicroseconds) { csStage(stageOut, @"selection_validation"); csError(errorOut, @"selected asset modification instant does not match PhotoKit"); return 0; }
    PHImageRequestOptions *options = [[PHImageRequestOptions alloc] init]; options.version = PHImageRequestOptionsVersionCurrent; options.deliveryMode = PHImageRequestOptionsDeliveryModeHighQualityFormat; options.resizeMode = PHImageRequestOptionsResizeModeNone; options.networkAccessAllowed = allowNetwork != 0; options.synchronous = NO;
    dispatch_semaphore_t done = dispatch_semaphore_create(0);
    __block NSData *data = nil;
    __block NSString *uti = nil;
    __block CGImagePropertyOrientation orientation = kCGImagePropertyOrientationUp;
    __block NSDictionary *info = nil;
    __block BOOL finished = NO;
    __block BOOL sawDegraded = NO;
    __block BOOL sawInCloud = NO;
    PHImageRequestID requestID = [[PHImageManager defaultManager] requestImageDataAndOrientationForAsset:asset options:options resultHandler:^(NSData *result, NSString *resultUTI, CGImagePropertyOrientation resultOrientation, NSDictionary *resultInfo) {
      @synchronized (done) {
        if (finished) return;
        BOOL degraded = [resultInfo[PHImageResultIsDegradedKey] boolValue];
        BOOL cancelled = [resultInfo[PHImageCancelledKey] boolValue];
        NSError *callbackError = resultInfo[PHImageErrorKey];
        sawDegraded = sawDegraded || degraded;
        sawInCloud = sawInCloud || [resultInfo[PHImageResultIsInCloudKey] boolValue];
        if (degraded && !cancelled && callbackError == nil) return;
        finished = YES;
        data = result;
        uti = resultUTI;
        orientation = resultOrientation;
        info = resultInfo;
        dispatch_semaphore_signal(done);
      }
    }];
    if (dispatch_semaphore_wait(done, dispatch_time(DISPATCH_TIME_NOW, timeoutMilliseconds * NSEC_PER_MSEC)) != 0) {
      @synchronized (done) {
        finished = YES;
        if (callbackDegradedOut) *callbackDegradedOut = sawDegraded;
        if (callbackInCloudOut) *callbackInCloudOut = sawInCloud;
      }
      [[PHImageManager defaultManager] cancelImageRequest:requestID];
      csError(errorOut, @"photokit original export timed out");
      return 0;
    }
    if ([info[PHImageCancelledKey] boolValue] || info[PHImageErrorKey] != nil || data.length == 0) {
      NSError *error = info[PHImageErrorKey];
      if (callbackReturnedOut) *callbackReturnedOut = 1;
      if (callbackCancelledOut) *callbackCancelledOut = [info[PHImageCancelledKey] boolValue];
      if (callbackDegradedOut) *callbackDegradedOut = sawDegraded || [info[PHImageResultIsDegradedKey] boolValue];
      if (callbackInCloudOut) *callbackInCloudOut = sawInCloud || [info[PHImageResultIsInCloudKey] boolValue];
      if (error != nil && errorDomainOut) csError(errorDomainOut, error.domain);
      if (error != nil && errorCodeOut) *errorCodeOut = error.code;
      csError(errorOut, @"PhotoKit current-still callback did not return a final image");
      return 0;
    }
    CGImageSourceRef source = CGImageSourceCreateWithData((__bridge CFDataRef)data, NULL); if (source == NULL) { csStage(stageOut, @"image_decode"); csError(errorOut, @"PhotoKit current-still bytes are not an image"); return 0; }
    NSDictionary *properties = CFBridgingRelease(CGImageSourceCopyPropertiesAtIndex(source, 0, NULL)); CFRelease(source); NSNumber *width = properties[(NSString *)kCGImagePropertyPixelWidth]; NSNumber *height = properties[(NSString *)kCGImagePropertyPixelHeight]; if (width == nil || height == nil || width.longLongValue <= 0 || height.longLongValue <= 0) { csStage(stageOut, @"image_dimensions"); csError(errorOut, @"PhotoKit current-still image dimensions are invalid"); return 0; }
    if (![data writeToFile:path options:NSDataWritingAtomic error:nil]) { csStage(stageOut, @"output_write"); csError(errorOut, @"write PhotoKit current-still bytes"); return 0; }
    if (mediaTypeOut) csError(mediaTypeOut, uti ?: @"public.image"); if (orientationOut) *orientationOut = orientation; if (pixelWidthOut) *pixelWidthOut = width.longLongValue; if (pixelHeightOut) *pixelHeightOut = height.longLongValue; return 1;
  }
}
