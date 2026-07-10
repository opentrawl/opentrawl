#import <Foundation/Foundation.h>
#import <AppKit/AppKit.h>
#import <Photos/Photos.h>
#import <CoreLocation/CoreLocation.h>
#import <CoreImage/CoreImage.h>
#import <CoreGraphics/CoreGraphics.h>
#import <ImageIO/ImageIO.h>
#import <dispatch/dispatch.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>

int photoscrawl_export_original_resource_matching(const char *localIdentifier, const char *creationDate, long long width, long long height, const char *originalFilename, const char *destinationPath, int allowNetwork, long long timeoutMilliseconds, char **errorOut, char **errorDomainOut, long long *errorCodeOut);

static NSString *pcString(NSString *value) {
  return value == nil ? @"" : value;
}

static NSString *pcDate(NSDate *date) {
  if (date == nil) {
    return @"";
  }
  static NSISO8601DateFormatter *formatter = nil;
  static dispatch_once_t onceToken;
  dispatch_once(&onceToken, ^{
    formatter = [[NSISO8601DateFormatter alloc] init];
    formatter.formatOptions = NSISO8601DateFormatWithInternetDateTime | NSISO8601DateFormatWithFractionalSeconds;
    formatter.timeZone = [NSTimeZone timeZoneWithName:@"UTC"];
  });
  return [formatter stringFromDate:date];
}

static NSString *pcMediaType(PHAssetMediaType mediaType) {
  switch (mediaType) {
    case PHAssetMediaTypeImage:
      return @"image";
    case PHAssetMediaTypeVideo:
      return @"video";
    case PHAssetMediaTypeAudio:
      return @"audio";
    case PHAssetMediaTypeUnknown:
    default:
      return @"unknown";
  }
}

static NSString *pcResourceType(PHAssetResourceType resourceType) {
  switch (resourceType) {
    case PHAssetResourceTypePhoto:
      return @"photo";
    case PHAssetResourceTypeVideo:
      return @"video";
    case PHAssetResourceTypeAudio:
      return @"audio";
    case PHAssetResourceTypeAlternatePhoto:
      return @"alternate_photo";
    case PHAssetResourceTypeFullSizePhoto:
      return @"full_size_photo";
    case PHAssetResourceTypeFullSizeVideo:
      return @"full_size_video";
    case PHAssetResourceTypeAdjustmentData:
      return @"adjustment_data";
    case PHAssetResourceTypeAdjustmentBasePhoto:
      return @"adjustment_base_photo";
    case PHAssetResourceTypePairedVideo:
      return @"paired_video";
    case PHAssetResourceTypeFullSizePairedVideo:
      return @"full_size_paired_video";
    case PHAssetResourceTypeAdjustmentBasePairedVideo:
      return @"adjustment_base_paired_video";
    case PHAssetResourceTypeAdjustmentBaseVideo:
      return @"adjustment_base_video";
    case PHAssetResourceTypePhotoProxy:
      return @"photo_proxy";
    default:
      return [NSString stringWithFormat:@"resource_type_%ld", (long)resourceType];
  }
}

static NSString *pcAuthorizationStatus(PHAuthorizationStatus status) {
  switch (status) {
    case PHAuthorizationStatusNotDetermined:
      return @"not_determined";
    case PHAuthorizationStatusRestricted:
      return @"restricted";
    case PHAuthorizationStatusDenied:
      return @"denied";
    case PHAuthorizationStatusAuthorized:
      return @"authorized";
    case PHAuthorizationStatusLimited:
      return @"limited";
    default:
      return [NSString stringWithFormat:@"status_%ld", (long)status];
  }
}

static char *pcCopyCString(NSString *value) {
  const char *utf8 = [pcString(value) UTF8String];
  if (utf8 == NULL) {
    utf8 = "";
  }
  return strdup(utf8);
}

static void pcSetError(char **errorOut, NSString *message) {
  if (errorOut == NULL) {
    return;
  }
  *errorOut = pcCopyCString(message);
}

static BOOL pcEnsureParentDirectory(NSURL *url, char **errorOut) {
  NSURL *parent = [url URLByDeletingLastPathComponent];
  if (parent == nil) {
    pcSetError(errorOut, @"missing destination parent directory");
    return NO;
  }
  NSError *error = nil;
  if (![[NSFileManager defaultManager] createDirectoryAtURL:parent withIntermediateDirectories:YES attributes:nil error:&error]) {
    pcSetError(errorOut, [NSString stringWithFormat:@"create destination directory: %@", error.localizedDescription]);
    return NO;
  }
  return YES;
}

static id pcJSONSafe(id value) {
  if (value == nil || value == (id)kCFNull) {
    return [NSNull null];
  }
  if ([value isKindOfClass:[NSString class]] || [value isKindOfClass:[NSNumber class]] || [value isKindOfClass:[NSNull class]]) {
    return value;
  }
  if ([value isKindOfClass:[NSArray class]]) {
    NSMutableArray *out = [NSMutableArray array];
    for (id item in (NSArray *)value) {
      [out addObject:pcJSONSafe(item)];
    }
    return out;
  }
  if ([value isKindOfClass:[NSDictionary class]]) {
    NSMutableDictionary *out = [NSMutableDictionary dictionary];
    for (id key in (NSDictionary *)value) {
      NSString *stringKey = [key isKindOfClass:[NSString class]] ? key : [key description];
      out[stringKey] = pcJSONSafe([(NSDictionary *)value objectForKey:key]);
    }
    return out;
  }
  return [value description];
}

static PHAuthorizationStatus pcCurrentAuthorizationStatus(void) {
  if (@available(macOS 11.0, *)) {
    // macOS Photos exposes asset fetch access through ReadWrite; AddOnly cannot
    // enumerate the library. This bridge still only calls fetch/read APIs.
    return [PHPhotoLibrary authorizationStatusForAccessLevel:PHAccessLevelReadWrite];
  }

  return [PHPhotoLibrary authorizationStatus];
}

static PHAuthorizationStatus pcRequestAuthorization(void) {
  __block PHAuthorizationStatus status = pcCurrentAuthorizationStatus();
  if (status != PHAuthorizationStatusNotDetermined) {
    return status;
  }

  NSApplication *application = [NSApplication sharedApplication];
  [application setActivationPolicy:NSApplicationActivationPolicyRegular];
  [application activateIgnoringOtherApps:YES];

  void (^completeRequest)(PHAuthorizationStatus) = ^(PHAuthorizationStatus requestedStatus) {
    dispatch_async(dispatch_get_main_queue(), ^{
      status = requestedStatus;
      [application stop:nil];
    });
  };
  if (@available(macOS 11.0, *)) {
    [PHPhotoLibrary requestAuthorizationForAccessLevel:PHAccessLevelReadWrite handler:^(PHAuthorizationStatus requestedStatus) {
      completeRequest(requestedStatus);
    }];
  } else {
    [PHPhotoLibrary requestAuthorization:^(PHAuthorizationStatus requestedStatus) {
      completeRequest(requestedStatus);
    }];
  }
  [application run];
  return status;
}

static BOOL pcRequireAuthorization(PHAuthorizationStatus status, char **errorOut) {
  if (status == PHAuthorizationStatusAuthorized || status == PHAuthorizationStatusLimited) {
    return YES;
  }
  pcSetError(errorOut, [NSString stringWithFormat:@"photos_access:%@", pcAuthorizationStatus(status)]);
  return NO;
}

static NSDictionary *pcLocationDictionary(CLLocation *location) {
  if (location == nil) {
    return nil;
  }
  NSMutableDictionary *out = [NSMutableDictionary dictionary];
  out[@"latitude"] = @(location.coordinate.latitude);
  out[@"longitude"] = @(location.coordinate.longitude);
  out[@"altitude"] = @(location.altitude);
  out[@"horizontal_accuracy"] = @(location.horizontalAccuracy);
  return out;
}

static NSArray *pcResources(PHAsset *asset) {
  NSMutableArray *out = [NSMutableArray array];
  for (PHAssetResource *resource in [PHAssetResource assetResourcesForAsset:asset]) {
    NSMutableDictionary *entry = [NSMutableDictionary dictionary];
    entry[@"type"] = pcResourceType(resource.type);
    entry[@"uti"] = pcString(resource.uniformTypeIdentifier);
    entry[@"original_filename"] = pcString(resource.originalFilename);
    entry[@"availability"] = @"unknown";
    entry[@"metadata"] = @{
      @"asset_local_identifier": pcString(resource.assetLocalIdentifier),
      @"content_availability_source": @"photokit_metadata_only"
    };
    [out addObject:entry];
  }
  return out;
}

static PHAssetResource *pcPreferredOriginalResource(PHAsset *asset) {
  for (PHAssetResource *resource in [PHAssetResource assetResourcesForAsset:asset]) {
    if (resource.type == PHAssetResourceTypePhoto) {
      return resource;
    }
  }
  return nil;
}

static NSString *pcIdentifierUUID(NSString *identifier) {
  NSString *trimmed = [identifier stringByTrimmingCharactersInSet:[NSCharacterSet whitespaceAndNewlineCharacterSet]];
  if (trimmed.length < 36) {
    return @"";
  }
  NSString *candidate = [[trimmed substringToIndex:36] uppercaseString];
  NSCharacterSet *hex = [NSCharacterSet characterSetWithCharactersInString:@"0123456789ABCDEF"];
  for (NSUInteger i = 0; i < candidate.length; i++) {
    unichar ch = [candidate characterAtIndex:i];
    if (i == 8 || i == 13 || i == 18 || i == 23) {
      if (ch != '-') {
        return @"";
      }
      continue;
    }
    if (![hex characterIsMember:ch]) {
      return @"";
    }
  }
  return candidate;
}

static PHFetchOptions *pcAssetFetchOptions(void) {
  PHFetchOptions *options = [[PHFetchOptions alloc] init];
  options.includeHiddenAssets = YES;
  options.wantsIncrementalChangeDetails = NO;
  if (@available(macOS 10.15, *)) {
    options.includeAllBurstAssets = YES;
  }
  return options;
}

static NSDictionary *pcAssetIdentifierIndex(void) {
  static NSDictionary *index = nil;
  static dispatch_once_t onceToken;
  dispatch_once(&onceToken, ^{
    NSMutableDictionary *out = [NSMutableDictionary dictionary];
    PHFetchResult<PHAsset *> *fetch = [PHAsset fetchAssetsWithOptions:pcAssetFetchOptions()];
    [fetch enumerateObjectsUsingBlock:^(PHAsset *asset, NSUInteger idx, BOOL *stop) {
      NSString *uuid = pcIdentifierUUID(asset.localIdentifier);
      if (uuid.length > 0 && out[uuid] == nil) {
        out[uuid] = pcString(asset.localIdentifier);
      }
    }];
    index = [out copy];
  });
  return index;
}

static PHAsset *pcFetchAssetForIdentifier(NSString *identifier) {
  PHFetchOptions *options = pcAssetFetchOptions();
  PHFetchResult<PHAsset *> *direct = [PHAsset fetchAssetsWithLocalIdentifiers:@[identifier] options:options];
  if (direct.firstObject != nil) {
    return direct.firstObject;
  }
  NSString *uuid = pcIdentifierUUID(identifier);
  if (uuid.length == 0) {
    return nil;
  }
  NSString *fullIdentifier = [pcAssetIdentifierIndex() objectForKey:uuid];
  if (fullIdentifier.length == 0) {
    return nil;
  }
  PHFetchResult<PHAsset *> *resolved = [PHAsset fetchAssetsWithLocalIdentifiers:@[fullIdentifier] options:options];
  return resolved.firstObject;
}

static int pcWriteOriginalResource(PHAsset *asset, NSString *path, int allowNetwork, long long timeoutMilliseconds, char **errorOut, char **errorDomainOut, long long *errorCodeOut) {
  if (asset == nil) {
    pcSetError(errorOut, @"PhotoKit asset not found");
    return 0;
  }
  PHAssetResource *resource = pcPreferredOriginalResource(asset);
  if (resource == nil) {
    pcSetError(errorOut, @"PhotoKit asset has no image original resource");
    return 0;
  }

  NSURL *destination = [NSURL fileURLWithPath:path];
  if (!pcEnsureParentDirectory(destination, errorOut)) {
    return 0;
  }
  [[NSFileManager defaultManager] removeItemAtURL:destination error:nil];

  PHAssetResourceRequestOptions *options = [[PHAssetResourceRequestOptions alloc] init];
  options.networkAccessAllowed = allowNetwork ? YES : NO;

  __block char *writeErrorMessage = NULL;
  __block char *writeErrorDomain = NULL;
  __block long long writeErrorCode = 0;
  dispatch_semaphore_t semaphore = dispatch_semaphore_create(0);
  [[PHAssetResourceManager defaultManager] writeDataForAssetResource:resource toFile:destination options:options completionHandler:^(NSError * _Nullable error) {
    if (error != nil) {
      writeErrorMessage = pcCopyCString(@"PhotoKit could not export the selected camera original");
      writeErrorDomain = pcCopyCString(error.domain);
      writeErrorCode = (long long)error.code;
    }
    dispatch_semaphore_signal(semaphore);
  }];
  if (timeoutMilliseconds <= 0) {
    pcSetError(errorOut, @"PhotoKit original export timed out");
    return 0;
  }
  dispatch_time_t deadline = dispatch_time(DISPATCH_TIME_NOW, timeoutMilliseconds * NSEC_PER_MSEC);
  if (dispatch_semaphore_wait(semaphore, deadline) != 0) {
    pcSetError(errorOut, @"PhotoKit original export timed out");
    return 0;
  }

  if (writeErrorMessage != NULL) {
    if (errorOut != NULL) {
      *errorOut = writeErrorMessage;
    } else {
      free(writeErrorMessage);
    }
    writeErrorMessage = NULL;
    if (errorDomainOut != NULL) {
      *errorDomainOut = writeErrorDomain;
    } else if (writeErrorDomain != NULL) {
      free(writeErrorDomain);
    }
    writeErrorDomain = NULL;
    if (errorCodeOut != NULL) {
      *errorCodeOut = writeErrorCode;
    }
    return 0;
  }
  return 1;
}

static NSArray *pcAlbums(PHAsset *asset) {
  NSMutableArray *out = [NSMutableArray array];
  PHFetchResult<PHAssetCollection *> *collections = [PHAssetCollection fetchAssetCollectionsContainingAsset:asset withType:PHAssetCollectionTypeAlbum options:nil];
  [collections enumerateObjectsUsingBlock:^(PHAssetCollection *collection, NSUInteger idx, BOOL *stop) {
    NSMutableDictionary *entry = [NSMutableDictionary dictionary];
    entry[@"album_id"] = pcString(collection.localIdentifier);
    entry[@"album_title"] = pcString(collection.localizedTitle);
    entry[@"album_kind"] = [NSString stringWithFormat:@"album:%ld:%ld", (long)collection.assetCollectionType, (long)collection.assetCollectionSubtype];
    [out addObject:entry];
  }];
  return out;
}

static NSDictionary *pcAssetDictionary(PHAsset *asset) {
  NSMutableDictionary *entry = [NSMutableDictionary dictionary];
  entry[@"local_identifier"] = pcString(asset.localIdentifier);
  entry[@"media_type"] = pcMediaType(asset.mediaType);
  entry[@"media_subtypes"] = [NSString stringWithFormat:@"%lu", (unsigned long)asset.mediaSubtypes];
  entry[@"creation_date"] = pcDate(asset.creationDate);
  entry[@"modification_date"] = pcDate(asset.modificationDate);
  entry[@"added_date"] = @"";
  entry[@"timezone_name"] = pcString([NSTimeZone localTimeZone].name);
  entry[@"width"] = @((long long)asset.pixelWidth);
  entry[@"height"] = @((long long)asset.pixelHeight);
  entry[@"duration_seconds"] = @(asset.duration);
  entry[@"favorite"] = @(asset.favorite);
  entry[@"hidden"] = @(asset.hidden);
  if (@available(macOS 10.15, *)) {
    entry[@"burst_identifier"] = pcString(asset.burstIdentifier);
    entry[@"represents_burst"] = @(asset.representsBurst);
  } else {
    entry[@"burst_identifier"] = @"";
    entry[@"represents_burst"] = @NO;
  }
  NSDictionary *location = pcLocationDictionary(asset.location);
  if (location != nil) {
    entry[@"location"] = location;
  }
  entry[@"resources"] = pcResources(asset);
  entry[@"albums"] = pcAlbums(asset);
  entry[@"metadata"] = @{
    @"photokit_local_identifier": pcString(asset.localIdentifier),
    @"source_type": @((long long)asset.sourceType)
  };
  return entry;
}

char *photoscrawl_request_photokit_authorization(char **errorOut) {
  @autoreleasepool {
    if (errorOut != NULL) {
      *errorOut = NULL;
    }
    if (!@available(macOS 10.15, *)) {
      pcSetError(errorOut, @"PhotoKit authorization requests require macOS 10.15 or newer");
      return NULL;
    }
    return pcCopyCString(pcAuthorizationStatus(pcRequestAuthorization()));
  }
}

char *photoscrawl_photokit_snapshot(const char *libraryPath, char **errorOut) {
  @autoreleasepool {
    if (errorOut != NULL) {
      *errorOut = NULL;
    }
    if (!@available(macOS 10.15, *)) {
      pcSetError(errorOut, @"PhotoKit sync requires macOS 10.15 or newer");
      return NULL;
    }

    NSString *path = libraryPath == NULL ? @"" : [NSString stringWithUTF8String:libraryPath];
    BOOL isDirectory = NO;
    if (path.length == 0 || ![[NSFileManager defaultManager] fileExistsAtPath:path isDirectory:&isDirectory] || !isDirectory) {
      pcSetError(errorOut, [NSString stringWithFormat:@"Photos library path does not exist or is not a directory: %@", path]);
      return NULL;
    }

    PHAuthorizationStatus status = pcCurrentAuthorizationStatus();
    if (!pcRequireAuthorization(status, errorOut)) {
      return NULL;
    }

    PHFetchOptions *options = pcAssetFetchOptions();
    options.sortDescriptors = @[
      [NSSortDescriptor sortDescriptorWithKey:@"creationDate" ascending:YES]
    ];

    PHFetchResult<PHAsset *> *fetch = [PHAsset fetchAssetsWithOptions:options];
    NSMutableArray *assets = [NSMutableArray arrayWithCapacity:fetch.count];
    [fetch enumerateObjectsUsingBlock:^(PHAsset *asset, NSUInteger idx, BOOL *stop) {
      [assets addObject:pcAssetDictionary(asset)];
    }];

    NSBundle *photosBundle = [NSBundle bundleWithIdentifier:@"com.apple.Photos"];
    NSString *photosVersion = [photosBundle objectForInfoDictionaryKey:@"CFBundleShortVersionString"];
    NSMutableDictionary *snapshot = [NSMutableDictionary dictionary];
    snapshot[@"library_path"] = path;
    snapshot[@"provider"] = @"photokit";
    snapshot[@"photos_version"] = pcString(photosVersion);
    snapshot[@"authorization_status"] = pcAuthorizationStatus(status);
    snapshot[@"metadata"] = @{
      @"source": @"PHPhotoLibrary.sharedPhotoLibrary",
      @"requested_library_path": path,
      @"library_path_note": @"PhotoKit enumerates the active system Photos library; this path is recorded as the requested source."
    };
    snapshot[@"assets"] = assets;

    NSError *jsonError = nil;
    NSData *data = [NSJSONSerialization dataWithJSONObject:snapshot options:0 error:&jsonError];
    if (data == nil) {
      pcSetError(errorOut, [NSString stringWithFormat:@"encode PhotoKit snapshot: %@", jsonError.localizedDescription]);
      return NULL;
    }
    char *json = malloc(data.length + 1);
    if (json == NULL) {
      pcSetError(errorOut, @"allocate PhotoKit JSON snapshot");
      return NULL;
    }
    memcpy(json, data.bytes, data.length);
    json[data.length] = '\0';
    return json;
  }
}

int photoscrawl_export_original_resource_matching(const char *localIdentifier, const char *creationDate, long long width, long long height, const char *originalFilename, const char *destinationPath, int allowNetwork, long long timeoutMilliseconds, char **errorOut, char **errorDomainOut, long long *errorCodeOut) {
  @autoreleasepool {
    if (errorOut != NULL) {
      *errorOut = NULL;
    }
    if (errorDomainOut != NULL) {
      *errorDomainOut = NULL;
    }
    if (errorCodeOut != NULL) {
      *errorCodeOut = 0;
    }
    if (!@available(macOS 10.15, *)) {
      pcSetError(errorOut, @"PhotoKit export requires macOS 10.15 or newer");
      return 0;
    }
    (void)creationDate;
    (void)width;
    (void)height;
    (void)originalFilename;
    NSString *identifier = localIdentifier == NULL ? @"" : [NSString stringWithUTF8String:localIdentifier];
    NSString *path = destinationPath == NULL ? @"" : [NSString stringWithUTF8String:destinationPath];
    if (identifier.length == 0 || path.length == 0) {
      pcSetError(errorOut, @"asset identifier and destination path are required");
      return 0;
    }

    PHAuthorizationStatus status = pcCurrentAuthorizationStatus();
    if (!pcRequireAuthorization(status, errorOut)) {
      return 0;
    }

    PHAsset *asset = pcFetchAssetForIdentifier(identifier);
    return pcWriteOriginalResource(asset, path, allowNetwork, timeoutMilliseconds, errorOut, errorDomainOut, errorCodeOut);
  }
}

int photoscrawl_render_canonical_jpeg(const char *sourcePath, const char *destinationPath, double quality, char **errorOut) {
  @autoreleasepool {
    if (errorOut != NULL) {
      *errorOut = NULL;
    }
    NSString *source = sourcePath == NULL ? @"" : [NSString stringWithUTF8String:sourcePath];
    NSString *destinationPathString = destinationPath == NULL ? @"" : [NSString stringWithUTF8String:destinationPath];
    if (source.length == 0 || destinationPathString.length == 0) {
      pcSetError(errorOut, @"source and destination paths are required");
      return 0;
    }

    NSURL *sourceURL = [NSURL fileURLWithPath:source];
    NSURL *destinationURL = [NSURL fileURLWithPath:destinationPathString];
    if (!pcEnsureParentDirectory(destinationURL, errorOut)) {
      return 0;
    }

    CIImage *image = [CIImage imageWithContentsOfURL:sourceURL options:@{ kCIImageApplyOrientationProperty: @YES }];
    if (image == nil || CGRectIsEmpty(image.extent)) {
      pcSetError(errorOut, @"load source image for canonical render");
      return 0;
    }

    CIContext *context = [CIContext contextWithOptions:nil];
    CGImageRef cgImage = [context createCGImage:image fromRect:image.extent];
    if (cgImage == NULL) {
      pcSetError(errorOut, @"create canonical CGImage");
      return 0;
    }

    [[NSFileManager defaultManager] removeItemAtURL:destinationURL error:nil];
    CGImageDestinationRef destination = CGImageDestinationCreateWithURL((__bridge CFURLRef)destinationURL, CFSTR("public.jpeg"), 1, NULL);
    if (destination == NULL) {
      CGImageRelease(cgImage);
      pcSetError(errorOut, @"create JPEG destination");
      return 0;
    }

    double boundedQuality = quality;
    if (boundedQuality <= 0 || boundedQuality > 1) {
      boundedQuality = 0.92;
    }
    NSDictionary *properties = @{
      (NSString *)kCGImageDestinationLossyCompressionQuality: @(boundedQuality),
      (NSString *)kCGImagePropertyOrientation: @1
    };
    CGImageDestinationAddImage(destination, cgImage, (__bridge CFDictionaryRef)properties);
    BOOL ok = CGImageDestinationFinalize(destination);
    CFRelease(destination);
    CGImageRelease(cgImage);
    if (!ok) {
      pcSetError(errorOut, @"write canonical JPEG");
      return 0;
    }
    return 1;
  }
}

char *photoscrawl_image_metadata_json(const char *sourcePath, char **errorOut) {
  @autoreleasepool {
    if (errorOut != NULL) {
      *errorOut = NULL;
    }
    NSString *source = sourcePath == NULL ? @"" : [NSString stringWithUTF8String:sourcePath];
    if (source.length == 0) {
      pcSetError(errorOut, @"source path is required");
      return NULL;
    }
    NSURL *sourceURL = [NSURL fileURLWithPath:source];
    CGImageSourceRef imageSource = CGImageSourceCreateWithURL((__bridge CFURLRef)sourceURL, NULL);
    if (imageSource == NULL) {
      pcSetError(errorOut, @"open source image metadata");
      return NULL;
    }
    NSDictionary *properties = CFBridgingRelease(CGImageSourceCopyPropertiesAtIndex(imageSource, 0, NULL));
    CFRelease(imageSource);
    if (properties == nil) {
      properties = @{};
    }
    id safe = pcJSONSafe(properties);
    NSError *jsonError = nil;
    NSData *data = [NSJSONSerialization dataWithJSONObject:safe options:0 error:&jsonError];
    if (data == nil) {
      pcSetError(errorOut, [NSString stringWithFormat:@"encode image metadata: %@", jsonError.localizedDescription]);
      return NULL;
    }
    char *json = malloc(data.length + 1);
    if (json == NULL) {
      pcSetError(errorOut, @"allocate image metadata JSON");
      return NULL;
    }
    memcpy(json, data.bytes, data.length);
    json[data.length] = '\0';
    return json;
  }
}
