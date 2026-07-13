#import <Foundation/Foundation.h>
#import <Photos/Photos.h>
#import <ImageIO/ImageIO.h>
#import <dispatch/dispatch.h>
#include <math.h>
#include <stdlib.h>
#include <string.h>

@class CSCurrentStillState;
static NSCondition *csCommandCondition;
static BOOL csCommandFinished;
static BOOL csCancellationRequested;
static CSCurrentStillState *csActiveState;
void photoscrawl_cancel_current_still_request(void);

int photoscrawl_prepare_current_still_main_loop(void) {
  if (![NSThread isMainThread]) return 0;
  csCommandCondition = [[NSCondition alloc] init];
  csCommandFinished = NO;
  csCancellationRequested = NO;
  csActiveState = nil;
  return 1;
}

void photoscrawl_run_current_still_main_loop(void) {
  if (![NSThread isMainThread]) return;
  while (true) {
    [csCommandCondition lock];
    BOOL finished = csCommandFinished;
    [csCommandCondition unlock];
    if (finished) return;
    CFRunLoopRunInMode(kCFRunLoopDefaultMode, 0.1, true);
  }
}

void photoscrawl_stop_current_still_main_loop(void) {
  [csCommandCondition lock];
  csCommandFinished = YES;
  [csCommandCondition signal];
  [csCommandCondition unlock];
  CFRunLoopWakeUp(CFRunLoopGetMain());
}

@interface CSCurrentStillState : NSObject
@property(nonatomic, strong) NSCondition *condition;
@property(nonatomic, strong) NSData *data;
@property(nonatomic, strong) NSString *uti;
@property(nonatomic, strong) NSDictionary *info;
@property(nonatomic, strong) PHImageManager *manager;
@property(nonatomic) PHImageRequestID requestID;
@property(nonatomic) CGImagePropertyOrientation orientation;
@property(nonatomic) BOOL started;
@property(nonatomic) BOOL finished;
@property(nonatomic) BOOL timedOut;
@property(nonatomic) BOOL cancelled;
@property(nonatomic) BOOL sawDegraded;
@property(nonatomic) BOOL sawInCloud;
@property(nonatomic) CFAbsoluteTime callbackStartedAt;
@property(nonatomic) CFAbsoluteTime callbackFinishedAt;
@end

@implementation CSCurrentStillState
@end

static BOOL csFinishCurrentStill(CSCurrentStillState *state, BOOL timedOut, BOOL cancelled, NSData *data, NSString *uti, CGImagePropertyOrientation orientation, NSDictionary *info) {
  [state.condition lock];
  if (state.finished) { [state.condition unlock]; return NO; }
  state.data = data;
  state.uti = uti;
  state.orientation = orientation;
  state.info = info;
  state.finished = YES;
  state.timedOut = timedOut;
  state.cancelled = cancelled;
  state.callbackFinishedAt = CFAbsoluteTimeGetCurrent();
  [state.condition unlock];
  return YES;
}

int photoscrawl_test_current_still_finish_once(int first, int second, int started, int *cancelCountOut, int *successCountOut) {
  CSCurrentStillState *state = [[CSCurrentStillState alloc] init];
  state.condition = [[NSCondition alloc] init];
  state.started = started != 0;
  int cancelCount = 0;
  int successCount = 0;
  for (int index = 0; index < 2; index++) {
    int outcome = index == 0 ? first : second;
    BOOL won = csFinishCurrentStill(state, outcome == 2, outcome == 3, outcome == 1 ? [NSData data] : nil, nil, kCGImagePropertyOrientationUp, nil);
    if (!won) continue;
    if (outcome == 1) successCount++;
    if ((outcome == 2 || outcome == 3) && state.started) cancelCount++;
  }
  if (cancelCountOut) *cancelCountOut = cancelCount;
  if (successCountOut) *successCountOut = successCount;
  return state.finished ? 1 : 0;
}

static BOOL csRegisterCurrentStillState(CSCurrentStillState *state) {
  [csCommandCondition lock];
  csActiveState = state;
  BOOL cancellationRequested = csCancellationRequested;
  csCancellationRequested = NO;
  [csCommandCondition unlock];
  return cancellationRequested;
}

int photoscrawl_test_current_still_cancel_before_registration(void) {
  csCommandCondition = [[NSCondition alloc] init];
  csActiveState = nil;
  csCancellationRequested = NO;
  photoscrawl_cancel_current_still_request();
  CSCurrentStillState *state = [[CSCurrentStillState alloc] init];
  state.condition = [[NSCondition alloc] init];
  return csRegisterCurrentStillState(state) && !state.started ? 1 : 0;
}

void photoscrawl_cancel_current_still_request(void) {
  [csCommandCondition lock];
  CSCurrentStillState *state = csActiveState;
  if (state == nil) csCancellationRequested = YES;
  [csCommandCondition unlock];
  if (state == nil) return;
  CFRunLoopPerformBlock(CFRunLoopGetMain(), kCFRunLoopDefaultMode, ^{
    if (!csFinishCurrentStill(state, NO, YES, nil, nil, kCGImagePropertyOrientationUp, nil)) return;
    [state.condition lock];
    PHImageManager *manager = state.manager;
    PHImageRequestID requestID = state.requestID;
    BOOL started = state.started;
    [state.condition unlock];
    if (started) [manager cancelImageRequest:requestID];
    [state.condition lock];
    [state.condition broadcast];
    [state.condition unlock];
  });
  CFRunLoopWakeUp(CFRunLoopGetMain());
}

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

static void csRecordElapsedMicros(long long *out, CFAbsoluteTime start) {
  if (out == NULL) return;
  *out = MAX(1, (long long)ceil((CFAbsoluteTimeGetCurrent() - start) * 1000000.0));
}

static NSString *csUUID(NSString *identifier) {
  return [[identifier componentsSeparatedByString:@"/"] firstObject].lowercaseString;
}

static PHAuthorizationStatus csStatus(void) {
  if (@available(macOS 11.0, *)) return [PHPhotoLibrary authorizationStatusForAccessLevel:PHAccessLevelReadWrite];
  return [PHPhotoLibrary authorizationStatus];
}

int photoscrawl_export_current_still_matching(const char *assetUUID, int hasExpectedModification, long long modificationUnixSeconds, int modificationMicroseconds, const char *destinationPath, int allowNetwork, long long timeoutMilliseconds, char **mediaTypeOut, long long *orientationOut, long long *pixelWidthOut, long long *pixelHeightOut, char **errorOut, char **errorDomainOut, long long *errorCodeOut, int *callbackCancelledOut, int *callbackDegradedOut, int *callbackInCloudOut, int *callbackReturnedOut, char **stageOut, long long *callbackMicrosOut, long long *validationMicrosOut, int *photoKitCallsOut) {
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
    if (callbackMicrosOut) *callbackMicrosOut = 0;
    if (validationMicrosOut) *validationMicrosOut = 0;
    if (photoKitCallsOut) *photoKitCallsOut = 0;
    NSString *uuid = assetUUID ? [NSString stringWithUTF8String:assetUUID] : @"";
    NSString *path = destinationPath ? [NSString stringWithUTF8String:destinationPath] : @"";
    if (uuid.length == 0 || path.length == 0 || (hasExpectedModification && (modificationUnixSeconds <= 0 || modificationMicroseconds < 0 || modificationMicroseconds >= 1000000))) { csError(errorOut, @"asset UUID, expected modification instant when present and destination path are required"); return 0; }
    PHAuthorizationStatus status = csStatus();
    if (status != PHAuthorizationStatusAuthorized && status != PHAuthorizationStatusLimited) { csError(errorOut, @"photos_access:denied"); return 0; }
    PHFetchOptions *fetchOptions = [[PHFetchOptions alloc] init];
    fetchOptions.includeHiddenAssets = YES;
    PHFetchResult<PHAsset *> *assets = [PHAsset fetchAssetsWithLocalIdentifiers:@[uuid] options:fetchOptions];
    PHAsset *asset = assets.firstObject;
    if (asset == nil) { csError(errorOut, @"photokit asset not found"); return 0; }
    if (hasExpectedModification) {
      if (asset.modificationDate == nil) { csStage(stageOut, @"selection_validation"); csError(errorOut, @"selected asset modification instant does not match PhotoKit"); return 0; }
      NSTimeInterval observed = asset.modificationDate.timeIntervalSince1970;
      long long observedSeconds = (long long)floor(observed);
      long long observedMicroseconds = llround((observed - observedSeconds) * 1000000.0);
      if (observedMicroseconds == 1000000) { observedSeconds++; observedMicroseconds = 0; }
      if (observedSeconds != modificationUnixSeconds || observedMicroseconds != modificationMicroseconds) { csStage(stageOut, @"selection_validation"); csError(errorOut, @"selected asset modification instant does not match PhotoKit"); return 0; }
    }
    CSCurrentStillState *state = [[CSCurrentStillState alloc] init];
    state.condition = [[NSCondition alloc] init];
    state.orientation = kCGImagePropertyOrientationUp;
    BOOL cancellationRequested = csRegisterCurrentStillState(state);
    if (cancellationRequested) csFinishCurrentStill(state, NO, YES, nil, nil, kCGImagePropertyOrientationUp, nil);
    if (cancellationRequested) goto currentStillFinished;
    CFRunLoopPerformBlock(CFRunLoopGetMain(), kCFRunLoopDefaultMode, ^{
      [state.condition lock];
      if (state.finished) { [state.condition unlock]; return; }
      [state.condition unlock];
      PHImageRequestOptions *options = [[PHImageRequestOptions alloc] init]; options.version = PHImageRequestOptionsVersionCurrent; options.deliveryMode = PHImageRequestOptionsDeliveryModeHighQualityFormat; options.resizeMode = PHImageRequestOptionsResizeModeNone; options.networkAccessAllowed = allowNetwork != 0; options.synchronous = NO;
      PHImageManager *manager = [PHImageManager defaultManager];
      state.callbackStartedAt = CFAbsoluteTimeGetCurrent();
      if (photoKitCallsOut) *photoKitCallsOut = 1;
      PHImageRequestID requestID = [manager requestImageDataAndOrientationForAsset:asset options:options resultHandler:^(NSData *result, NSString *resultUTI, CGImagePropertyOrientation resultOrientation, NSDictionary *resultInfo) {
        [state.condition lock];
        if (state.finished) { [state.condition unlock]; return; }
        BOOL degraded = [resultInfo[PHImageResultIsDegradedKey] boolValue];
        BOOL cancelled = [resultInfo[PHImageCancelledKey] boolValue];
        NSError *callbackError = resultInfo[PHImageErrorKey];
        state.sawDegraded = state.sawDegraded || degraded;
        state.sawInCloud = state.sawInCloud || [resultInfo[PHImageResultIsInCloudKey] boolValue];
        if (degraded && !cancelled && callbackError == nil) { [state.condition unlock]; return; }
        [state.condition unlock];
        if (!csFinishCurrentStill(state, NO, NO, result, resultUTI, resultOrientation, resultInfo)) return;
        [state.condition lock];
        [state.condition broadcast];
        [state.condition unlock];
      }];
      [state.condition lock];
      state.manager = manager;
      state.requestID = requestID;
      state.started = YES;
      BOOL cancelAfterStart = state.cancelled || state.timedOut;
      [state.condition unlock];
      if (cancelAfterStart) [manager cancelImageRequest:requestID];
      CFRunLoopTimerRef timer = CFRunLoopTimerCreateWithHandler(kCFAllocatorDefault, CFAbsoluteTimeGetCurrent() + (CFAbsoluteTime)timeoutMilliseconds / 1000.0, 0, 0, 0, ^(CFRunLoopTimerRef unused) {
        if (!csFinishCurrentStill(state, YES, NO, nil, nil, kCGImagePropertyOrientationUp, nil)) return;
        [state.condition lock];
        PHImageManager *manager = state.manager;
        PHImageRequestID requestID = state.requestID;
        BOOL started = state.started;
        [state.condition unlock];
        if (started) [manager cancelImageRequest:requestID];
        [state.condition lock];
        [state.condition broadcast];
        [state.condition unlock];
      });
      CFRunLoopAddTimer(CFRunLoopGetMain(), timer, kCFRunLoopDefaultMode);
      CFRelease(timer);
    });
    CFRunLoopWakeUp(CFRunLoopGetMain());
currentStillFinished:
    [state.condition lock];
    while (!state.finished) [state.condition wait];
    BOOL timedOut = state.timedOut;
    BOOL cancelled = state.cancelled;
    NSData *data = state.data;
    NSString *uti = state.uti;
    CGImagePropertyOrientation orientation = state.orientation;
    NSDictionary *info = state.info;
    BOOL sawDegraded = state.sawDegraded;
    BOOL sawInCloud = state.sawInCloud;
    CFAbsoluteTime callbackStartedAt = state.callbackStartedAt;
    CFAbsoluteTime callbackFinishedAt = state.callbackFinishedAt;
    [state.condition unlock];
    if (callbackMicrosOut && callbackStartedAt > 0 && callbackFinishedAt >= callbackStartedAt) *callbackMicrosOut = MAX(1, (long long)ceil((callbackFinishedAt - callbackStartedAt) * 1000000.0));
    [csCommandCondition lock];
    if (csActiveState == state) csActiveState = nil;
    [csCommandCondition unlock];
    if (cancelled) {
      csError(errorOut, @"photokit current-still request cancelled");
      return 0;
    }
    if (timedOut) {
      if (callbackDegradedOut) *callbackDegradedOut = sawDegraded;
      if (callbackInCloudOut) *callbackInCloudOut = sawInCloud;
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
    CFAbsoluteTime validationStartedAt = CFAbsoluteTimeGetCurrent();
    CGImageSourceRef source = CGImageSourceCreateWithData((__bridge CFDataRef)data, NULL);
    if (source == NULL) {
      csRecordElapsedMicros(validationMicrosOut, validationStartedAt);
      csStage(stageOut, @"image_decode");
      csError(errorOut, @"PhotoKit current-still bytes are not an image");
      return 0;
    }
    NSDictionary *properties = CFBridgingRelease(CGImageSourceCopyPropertiesAtIndex(source, 0, NULL));
    CFRelease(source);
    NSNumber *width = properties[(NSString *)kCGImagePropertyPixelWidth];
    NSNumber *height = properties[(NSString *)kCGImagePropertyPixelHeight];
    if (width == nil || height == nil || width.longLongValue <= 0 || height.longLongValue <= 0) {
      csRecordElapsedMicros(validationMicrosOut, validationStartedAt);
      csStage(stageOut, @"image_dimensions");
      csError(errorOut, @"PhotoKit current-still image dimensions are invalid");
      return 0;
    }
    if (![data writeToFile:path options:NSDataWritingAtomic error:nil]) {
      csRecordElapsedMicros(validationMicrosOut, validationStartedAt);
      csStage(stageOut, @"output_write");
      csError(errorOut, @"write PhotoKit current-still bytes");
      return 0;
    }
    csRecordElapsedMicros(validationMicrosOut, validationStartedAt);
    if (mediaTypeOut) csError(mediaTypeOut, uti ?: @"public.image"); if (orientationOut) *orientationOut = orientation; if (pixelWidthOut) *pixelWidthOut = width.longLongValue; if (pixelHeightOut) *pixelHeightOut = height.longLongValue; return 1;
  }
}
