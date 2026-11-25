//go:build darwin

#import <AVFoundation/AVFoundation.h>
#import <CoreGraphics/CoreGraphics.h>
#import <Foundation/Foundation.h>
#import <ImageIO/ImageIO.h>
#import <Photos/Photos.h>
#include <dispatch/dispatch.h>
#include <math.h>
#include <stdlib.h>
#include <string.h>

#include "fetch_picture_native.h"

static const int32_t defaultPictureLongEdgePixelLimit = 2576;
static const double pictureJPEGQuality = 0.85;
static const int64_t applePhotosDeliveryTimeoutSeconds = 30;
static const int64_t applePhotosAuthorizationTimeoutSeconds = 60;

static NSInteger resolvedPictureLongEdgePixelLimit(int32_t requestedLimit) {
  if (requestedLimit <= 0) {
    return (NSInteger)defaultPictureLongEdgePixelLimit;
  }
  return (NSInteger)requestedLimit;
}

static NativeFetchedPicture failedPictureFetch(NativePictureFetchOutcome outcome,
                                               NSString *failureMessage) {
  NativeFetchedPicture fetchedPicture = {0};
  fetchedPicture.picture_fetch_outcome = outcome;
  fetchedPicture.picture_fetch_failure_message = strdup(failureMessage.UTF8String);
  return fetchedPicture;
}

// Guards the entire native fetch against any synchronous PhotoKit, ImageIO or
// AVFoundation call (such as XPC to photolibraryd or AVAsset.duration) hanging
// beyond the per-photo deadline.
static NativeFetchedPicture runPictureFetchWithHardTimeout(int64_t timeoutSeconds,
                                                           NSString *timeoutMessage,
                                                           NativeFetchedPicture (^work)(void)) {
  NSObject *completionLock = [[NSObject alloc] init];
  __block BOOL timedOut = NO;
  __block NativeFetchedPicture completedPicture = {0};
  dispatch_semaphore_t workDone = dispatch_semaphore_create(0);
  dispatch_async(dispatch_get_global_queue(QOS_CLASS_USER_INITIATED, 0), ^{
    @autoreleasepool {
      NativeFetchedPicture produced = work();
      BOOL discardAfterTimeout = NO;
      @synchronized(completionLock) {
        if (timedOut) {
          discardAfterTimeout = YES;
        } else {
          completedPicture = produced;
        }
      }
      if (discardAfterTimeout) {
        freeNativeFetchedPicture(produced);
      }
      dispatch_semaphore_signal(workDone);
    }
  });
  if (dispatch_semaphore_wait(workDone,
                              dispatch_time(DISPATCH_TIME_NOW, timeoutSeconds * NSEC_PER_SEC)) != 0) {
    @synchronized(completionLock) {
      timedOut = YES;
    }
    return failedPictureFetch(NATIVE_PICTURE_FETCH_OUTCOME_FAILED_TRY_AGAIN_LATER, timeoutMessage);
  }
  NativeFetchedPicture result = {0};
  @synchronized(completionLock) {
    result = completedPicture;
  }
  return result;
}

static NSString *applePhotosErrorDescription(NSDictionary *requestInfo, NSString *whatFailed) {
  NSError *requestError = requestInfo[PHImageErrorKey];
  if (requestError != nil) {
    return [NSString stringWithFormat:@"%@: %@ (%@ %ld)", whatFailed, requestError.localizedDescription,
                                      requestError.domain, (long)requestError.code];
  }
  if ([requestInfo[PHImageCancelledKey] boolValue]) {
    return [NSString stringWithFormat:@"%@: the request was cancelled", whatFailed];
  }
  return whatFailed;
}

// Drawing into a white-filled sRGB bitmap converts the colours and keeps
// transparent PNGs readable; the JPEG keeps no metadata, so the orientation
// is in the pixels.
static NativeFetchedPicture fetchedPictureFromUprightImage(CGImageRef uprightImage) {
  size_t pixelWidth = CGImageGetWidth(uprightImage);
  size_t pixelHeight = CGImageGetHeight(uprightImage);
  CGColorSpaceRef standardRGB = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
  CGContextRef bitmap = CGBitmapContextCreate(NULL, pixelWidth, pixelHeight, 8, 0, standardRGB,
                                              kCGImageAlphaNoneSkipLast | kCGBitmapByteOrder32Big);
  CGColorSpaceRelease(standardRGB);
  if (bitmap == NULL) {
    return failedPictureFetch(NATIVE_PICTURE_FETCH_OUTCOME_FAILED_TRY_AGAIN_LATER,
                              @"Core Graphics could not make an sRGB bitmap of the picture");
  }
  CGRect imageBounds = CGRectMake(0, 0, pixelWidth, pixelHeight);
  CGContextSetRGBFillColor(bitmap, 1.0, 1.0, 1.0, 1.0);
  CGContextFillRect(bitmap, imageBounds);
  CGContextSetInterpolationQuality(bitmap, kCGInterpolationHigh);
  CGContextDrawImage(bitmap, imageBounds, uprightImage);
  CGImageRef standardRGBImage = CGBitmapContextCreateImage(bitmap);
  CGContextRelease(bitmap);
  if (standardRGBImage == NULL) {
    return failedPictureFetch(NATIVE_PICTURE_FETCH_OUTCOME_FAILED_TRY_AGAIN_LATER,
                              @"Core Graphics could not copy the sRGB bitmap");
  }

  NSMutableData *jpegData = [NSMutableData data];
  CGImageDestinationRef jpegDestination =
      CGImageDestinationCreateWithData((__bridge CFMutableDataRef)jpegData, CFSTR("public.jpeg"), 1, NULL);
  if (jpegDestination == NULL) {
    CGImageRelease(standardRGBImage);
    return failedPictureFetch(NATIVE_PICTURE_FETCH_OUTCOME_FAILED_TRY_AGAIN_LATER,
                              @"ImageIO could not create a JPEG destination");
  }
  CGImageDestinationAddImage(jpegDestination, standardRGBImage, (__bridge CFDictionaryRef) @{
    (__bridge NSString *)kCGImageDestinationLossyCompressionQuality : @(pictureJPEGQuality),
  });
  bool jpegWasWritten = CGImageDestinationFinalize(jpegDestination);
  CFRelease(jpegDestination);
  CGImageRelease(standardRGBImage);
  if (!jpegWasWritten) {
    return failedPictureFetch(NATIVE_PICTURE_FETCH_OUTCOME_FAILED_TRY_AGAIN_LATER,
                              @"ImageIO could not write the picture as JPEG");
  }
  NativeFetchedPicture fetchedPicture = {0};
  fetchedPicture.picture_fetch_outcome = NATIVE_PICTURE_FETCH_OUTCOME_FETCHED;
  fetchedPicture.picture_jpeg_byte_count = jpegData.length;
  fetchedPicture.picture_jpeg_bytes = malloc(jpegData.length);
  memcpy(fetchedPicture.picture_jpeg_bytes, jpegData.bytes, jpegData.length);
  return fetchedPicture;
}

static void readCameraBearing(CGImageSourceRef pictureSource, size_t primaryImageIndex,
                              NativeFetchedPicture *fetchedPicture) {
  NSDictionary *pictureProperties =
      CFBridgingRelease(CGImageSourceCopyPropertiesAtIndex(pictureSource, primaryImageIndex, NULL));
  NSDictionary *gpsProperties = pictureProperties[(__bridge NSString *)kCGImagePropertyGPSDictionary];
  NSNumber *imageDirection = gpsProperties[(__bridge NSString *)kCGImagePropertyGPSImgDirection];
  NSString *imageDirectionReference = gpsProperties[(__bridge NSString *)kCGImagePropertyGPSImgDirectionRef];
  if ([imageDirection isKindOfClass:[NSNumber class]] && isfinite(imageDirection.doubleValue)) {
    fetchedPicture->camera_bearing_is_recorded = true;
    fetchedPicture->camera_bearing_degrees = imageDirection.doubleValue;
  }
  if ([imageDirectionReference isKindOfClass:[NSString class]] && imageDirectionReference.length > 0) {
    fetchedPicture->camera_bearing_reference_letter = (char)[imageDirectionReference characterAtIndex:0];
  }
}

static NativeFetchedPicture fetchedPictureFromImageSource(CGImageSourceRef pictureSource,
                                                          NSInteger pictureLongEdgePixelLimit) {
  if (CGImageSourceGetStatus(pictureSource) != kCGImageStatusComplete || CGImageSourceGetCount(pictureSource) == 0) {
    return failedPictureFetch(NATIVE_PICTURE_FETCH_OUTCOME_FAILED_TRY_AGAIN_LATER,
                              @"ImageIO could not read the picture");
  }
  size_t primaryImageIndex = CGImageSourceGetPrimaryImageIndex(pictureSource);
  CGImageRef uprightImage = CGImageSourceCreateThumbnailAtIndex(pictureSource, primaryImageIndex, (__bridge CFDictionaryRef) @{
    (__bridge NSString *)kCGImageSourceCreateThumbnailFromImageAlways : @YES,
    (__bridge NSString *)kCGImageSourceCreateThumbnailWithTransform : @YES,
    (__bridge NSString *)kCGImageSourceThumbnailMaxPixelSize : @(pictureLongEdgePixelLimit),
    (__bridge NSString *)kCGImageSourceShouldCacheImmediately : @YES,
  });
  if (uprightImage == NULL) {
    return failedPictureFetch(NATIVE_PICTURE_FETCH_OUTCOME_FAILED_TRY_AGAIN_LATER,
                              @"ImageIO could not decode the picture");
  }
  NativeFetchedPicture fetchedPicture = fetchedPictureFromUprightImage(uprightImage);
  CGImageRelease(uprightImage);
  if (fetchedPicture.picture_fetch_outcome == NATIVE_PICTURE_FETCH_OUTCOME_FETCHED) {
    readCameraBearing(pictureSource, primaryImageIndex, &fetchedPicture);
  }
  return fetchedPicture;
}

static NativeFetchedPicture fetchedPictureFromVideoMiddleFrame(AVAsset *video,
                                                               NSInteger pictureLongEdgePixelLimit) {
  AVAssetImageGenerator *frameGenerator = [AVAssetImageGenerator assetImageGeneratorWithAsset:video];
  frameGenerator.appliesPreferredTrackTransform = YES;
  frameGenerator.maximumSize = CGSizeMake(pictureLongEdgePixelLimit, pictureLongEdgePixelLimit);
  CMTime videoDuration = video.duration;
  CMTime middleTime = kCMTimeZero;
  if (CMTIME_IS_NUMERIC(videoDuration) && CMTimeCompare(videoDuration, kCMTimeZero) > 0) {
    middleTime = CMTimeMultiplyByRatio(videoDuration, 1, 2);
  }
  NSObject *completionLock = [[NSObject alloc] init];
  __block BOOL generationTimedOut = NO;
  __block CGImageRef middleFrame = NULL;
  __block NSError *frameError = nil;
  dispatch_semaphore_t frameWasGenerated = dispatch_semaphore_create(0);
  [frameGenerator generateCGImageAsynchronouslyForTime:middleTime
                                     completionHandler:^(CGImageRef frame, CMTime actualTime, NSError *error) {
                                       (void)actualTime;
                                       @synchronized(completionLock) {
                                         if (!generationTimedOut) {
                                           if (frame != NULL) {
                                             middleFrame = CGImageRetain(frame);
                                           }
                                           frameError = error;
                                         }
                                       }
                                       dispatch_semaphore_signal(frameWasGenerated);
                                     }];
  if (dispatch_semaphore_wait(frameWasGenerated,
                              dispatch_time(DISPATCH_TIME_NOW, applePhotosDeliveryTimeoutSeconds * NSEC_PER_SEC)) != 0) {
    CGImageRef frameToRelease = NULL;
    @synchronized(completionLock) {
      generationTimedOut = YES;
      frameToRelease = middleFrame;
      middleFrame = NULL;
    }
    [frameGenerator cancelAllCGImageGeneration];
    if (frameToRelease != NULL) {
      CGImageRelease(frameToRelease);
    }
    return failedPictureFetch(NATIVE_PICTURE_FETCH_OUTCOME_FAILED_TRY_AGAIN_LATER,
                              @"AVFoundation did not make a video frame within 30 seconds");
  }
  CGImageRef deliveredFrame = NULL;
  NSError *deliveredError = nil;
  @synchronized(completionLock) {
    deliveredFrame = middleFrame;
    middleFrame = NULL;
    deliveredError = frameError;
  }
  if (deliveredFrame == NULL) {
    return failedPictureFetch(NATIVE_PICTURE_FETCH_OUTCOME_FAILED_TRY_AGAIN_LATER,
                              [NSString stringWithFormat:@"AVFoundation could not make a video frame: %@",
                                                         deliveredError.localizedDescription]);
  }
  NativeFetchedPicture fetchedPicture = fetchedPictureFromUprightImage(deliveredFrame);
  CGImageRelease(deliveredFrame);
  return fetchedPicture;
}

NativeFetchedPicture fetchUprightPictureFromFile(const char *pictureFilePath,
                                                 bool pictureFileIsVideo,
                                                 int32_t pictureLongEdgePixelLimit) {
  @autoreleasepool {
    NSString *pathCopy = @(pictureFilePath);
    NSInteger pixelLimit = resolvedPictureLongEdgePixelLimit(pictureLongEdgePixelLimit);
    return runPictureFetchWithHardTimeout(
        applePhotosDeliveryTimeoutSeconds,
        @"Reading the picture file did not finish within 30 seconds",
        ^NativeFetchedPicture {
          NSURL *pictureFileURL = [NSURL fileURLWithPath:pathCopy];
          if (pictureFileIsVideo) {
            return fetchedPictureFromVideoMiddleFrame([AVURLAsset URLAssetWithURL:pictureFileURL options:nil], pixelLimit);
          }
          CGImageSourceRef pictureSource = CGImageSourceCreateWithURL((__bridge CFURLRef)pictureFileURL, NULL);
          if (pictureSource == NULL) {
            return failedPictureFetch(NATIVE_PICTURE_FETCH_OUTCOME_FAILED_TRY_AGAIN_LATER,
                                      @"ImageIO could not open the picture file");
          }
          NativeFetchedPicture fetchedPicture = fetchedPictureFromImageSource(pictureSource, pixelLimit);
          CFRelease(pictureSource);
          return fetchedPicture;
        });
  }
}

static PHAuthorizationStatus ensureApplePhotosAuthorization(void) {
  PHAuthorizationStatus status = [PHPhotoLibrary authorizationStatusForAccessLevel:PHAccessLevelReadWrite];
  if (status != PHAuthorizationStatusNotDetermined) {
    return status;
  }
  NSObject *authorizationLock = [[NSObject alloc] init];
  __block BOOL authorizationTimedOut = NO;
  __block PHAuthorizationStatus grantedStatus = status;
  dispatch_semaphore_t authorizationDone = dispatch_semaphore_create(0);
  [PHPhotoLibrary requestAuthorizationForAccessLevel:PHAccessLevelReadWrite
                                             handler:^(PHAuthorizationStatus updatedStatus) {
                                               @synchronized(authorizationLock) {
                                                 if (!authorizationTimedOut) {
                                                   grantedStatus = updatedStatus;
                                                 }
                                               }
                                               dispatch_semaphore_signal(authorizationDone);
                                             }];
  if (dispatch_semaphore_wait(authorizationDone,
                              dispatch_time(DISPATCH_TIME_NOW, applePhotosAuthorizationTimeoutSeconds * NSEC_PER_SEC)) != 0) {
    @synchronized(authorizationLock) {
      authorizationTimedOut = YES;
    }
    return [PHPhotoLibrary authorizationStatusForAccessLevel:PHAccessLevelReadWrite];
  }
  PHAuthorizationStatus resolvedStatus = status;
  @synchronized(authorizationLock) {
    resolvedStatus = grantedStatus;
  }
  return resolvedStatus;
}

// Still-image requests MUST set synchronous = YES because Go CLI processes do
// not pump the main dispatch queue or CFRunLoop, and PHImageManager dispatches
// asynchronous image resultHandlers to the main thread where they never run.
// Running the whole fetch inside runPictureFetchWithHardTimeout on a background
// GCD worker queue keeps synchronous PhotoKit/iCloud calls bounded. Video
// requests use requestAVAssetForVideo, whose handler runs on an arbitrary queue.
NativeFetchedPicture fetchUprightPictureFromApplePhotos(const char *applePhotoUUID,
                                                        int32_t pictureLongEdgePixelLimit) {
  @autoreleasepool {
    NSString *uuidCopy = @(applePhotoUUID);
    NSInteger pixelLimit = resolvedPictureLongEdgePixelLimit(pictureLongEdgePixelLimit);
    return runPictureFetchWithHardTimeout(
        applePhotosDeliveryTimeoutSeconds,
        @"Apple Photos did not deliver the picture within 30 seconds",
        ^NativeFetchedPicture {
          if (ensureApplePhotosAuthorization() != PHAuthorizationStatusAuthorized) {
            return failedPictureFetch(NATIVE_PICTURE_FETCH_OUTCOME_APPLE_PHOTOS_ACCESS_NOT_GRANTED,
                                      @"OpenTrawl does not have full access to Apple Photos");
          }
          PHFetchOptions *assetFetchOptions = [[PHFetchOptions alloc] init];
          assetFetchOptions.includeHiddenAssets = YES;
          assetFetchOptions.includeAllBurstAssets = YES;
          NSString *localIdentifier = [NSString stringWithFormat:@"%@/L0/001", uuidCopy];
          PHAsset *asset = [PHAsset fetchAssetsWithLocalIdentifiers:@[ localIdentifier ] options:assetFetchOptions].firstObject;
          if (asset == nil) {
            return failedPictureFetch(NATIVE_PICTURE_FETCH_OUTCOME_FAILED_FOR_GOOD,
                                      @"Apple Photos has no photo with this UUID");
          }
          if (asset.mediaType == PHAssetMediaTypeVideo) {
            PHVideoRequestOptions *videoRequestOptions = [[PHVideoRequestOptions alloc] init];
            videoRequestOptions.version = PHVideoRequestOptionsVersionCurrent;
            videoRequestOptions.deliveryMode = PHVideoRequestOptionsDeliveryModeMediumQualityFormat;
            videoRequestOptions.networkAccessAllowed = YES;
            NSObject *videoLock = [[NSObject alloc] init];
            __block BOOL videoTimedOut = NO;
            __block AVAsset *video = nil;
            __block NSDictionary *videoRequestInfo = nil;
            dispatch_semaphore_t videoWasDelivered = dispatch_semaphore_create(0);
            PHImageRequestID videoRequest = [[PHImageManager defaultManager]
                requestAVAssetForVideo:asset
                               options:videoRequestOptions
                         resultHandler:^(AVAsset *deliveredVideo, AVAudioMix *audioMix, NSDictionary *requestInfo) {
                           (void)audioMix;
                           @synchronized(videoLock) {
                             if (!videoTimedOut) {
                               video = deliveredVideo;
                               videoRequestInfo = requestInfo;
                             }
                           }
                           dispatch_semaphore_signal(videoWasDelivered);
                         }];
            if (dispatch_semaphore_wait(videoWasDelivered,
                                        dispatch_time(DISPATCH_TIME_NOW, applePhotosDeliveryTimeoutSeconds * NSEC_PER_SEC)) != 0) {
              @synchronized(videoLock) {
                videoTimedOut = YES;
              }
              [[PHImageManager defaultManager] cancelImageRequest:videoRequest];
              return failedPictureFetch(NATIVE_PICTURE_FETCH_OUTCOME_FAILED_TRY_AGAIN_LATER,
                                        @"Apple Photos did not deliver the video within 30 seconds");
            }
            AVAsset *deliveredVideo = nil;
            NSDictionary *deliveredVideoInfo = nil;
            @synchronized(videoLock) {
              deliveredVideo = video;
              deliveredVideoInfo = videoRequestInfo;
            }
            if (deliveredVideo == nil) {
              return failedPictureFetch(NATIVE_PICTURE_FETCH_OUTCOME_FAILED_TRY_AGAIN_LATER,
                                        applePhotosErrorDescription(deliveredVideoInfo, @"Apple Photos did not deliver the video"));
            }
            return fetchedPictureFromVideoMiddleFrame(deliveredVideo, pixelLimit);
          }
          PHImageRequestOptions *imageRequestOptions = [[PHImageRequestOptions alloc] init];
          imageRequestOptions.version = PHImageRequestOptionsVersionCurrent;
          imageRequestOptions.deliveryMode = PHImageRequestOptionsDeliveryModeHighQualityFormat;
          imageRequestOptions.networkAccessAllowed = YES;
          imageRequestOptions.synchronous = YES;
          __block NSData *deliveredPictureData = nil;
          __block NSDictionary *deliveredImageInfo = nil;
          [[PHImageManager defaultManager]
              requestImageDataAndOrientationForAsset:asset
                                             options:imageRequestOptions
                                       resultHandler:^(NSData *deliveredData, NSString *dataUTI,
                                                       CGImagePropertyOrientation orientation, NSDictionary *requestInfo) {
                                         (void)dataUTI;
                                         (void)orientation;
                                         deliveredPictureData = deliveredData;
                                         deliveredImageInfo = requestInfo;
                                       }];
          if (deliveredPictureData == nil) {
            return failedPictureFetch(NATIVE_PICTURE_FETCH_OUTCOME_FAILED_TRY_AGAIN_LATER,
                                      applePhotosErrorDescription(deliveredImageInfo, @"Apple Photos did not deliver the picture"));
          }
          CGImageSourceRef pictureSource = CGImageSourceCreateWithData((__bridge CFDataRef)deliveredPictureData, NULL);
          if (pictureSource == NULL) {
            return failedPictureFetch(NATIVE_PICTURE_FETCH_OUTCOME_FAILED_TRY_AGAIN_LATER,
                                      @"ImageIO could not open the picture Apple Photos delivered");
          }
          NativeFetchedPicture fetchedPicture = fetchedPictureFromImageSource(pictureSource, pixelLimit);
          CFRelease(pictureSource);
          return fetchedPicture;
        });
  }
}

void freeNativeFetchedPicture(NativeFetchedPicture fetchedPicture) {
  free(fetchedPicture.picture_fetch_failure_message);
  free(fetchedPicture.picture_jpeg_bytes);
}
