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

static NSDictionary *pcTypedMetadataValue(id value, NSString **errorOut) {
  if (value == nil || value == (id)kCFNull) {
    return @{ @"type": @"null" };
  }
  if ([value isKindOfClass:[NSString class]]) {
    return @{ @"type": @"string", @"string": value };
  }
  if ([value isKindOfClass:[NSDate class]]) {
    return @{ @"type": @"date", @"date": pcDate(value) };
  }
  if ([value isKindOfClass:[NSData class]]) {
    return @{ @"type": @"data", @"data": [(NSData *)value base64EncodedStringWithOptions:0] };
  }
  if ([value isKindOfClass:[NSNumber class]]) {
    if (CFGetTypeID((__bridge CFTypeRef)value) == CFBooleanGetTypeID()) {
      return @{ @"type": @"boolean", @"boolean": value };
    }
    const char *numberType = [(NSNumber *)value objCType];
    if (strcmp(numberType, @encode(float)) == 0 || strcmp(numberType, @encode(double)) == 0 || strcmp(numberType, @encode(long double)) == 0) {
      return @{ @"type": @"decimal", @"decimal": [(NSNumber *)value stringValue] };
    }
    if (strcmp(numberType, @encode(unsigned char)) == 0 || strcmp(numberType, @encode(unsigned short)) == 0 ||
        strcmp(numberType, @encode(unsigned int)) == 0 || strcmp(numberType, @encode(unsigned long)) == 0 ||
        strcmp(numberType, @encode(unsigned long long)) == 0) {
      return @{ @"type": @"unsigned_integer", @"unsigned_integer": [(NSNumber *)value stringValue] };
    }
    if (strcmp(numberType, @encode(char)) == 0 || strcmp(numberType, @encode(short)) == 0 ||
        strcmp(numberType, @encode(int)) == 0 || strcmp(numberType, @encode(long)) == 0 ||
        strcmp(numberType, @encode(long long)) == 0) {
      return @{ @"type": @"signed_integer", @"signed_integer": [(NSNumber *)value stringValue] };
    }
    if (errorOut != NULL) {
      *errorOut = [NSString stringWithFormat:@"unsupported ImageIO number type %s", numberType];
    }
    return nil;
  }
  if ([value isKindOfClass:[NSArray class]]) {
    NSMutableArray *items = [NSMutableArray arrayWithCapacity:[(NSArray *)value count]];
    for (id item in (NSArray *)value) {
      NSDictionary *typed = pcTypedMetadataValue(item, errorOut);
      if (typed == nil) {
        return nil;
      }
      [items addObject:typed];
    }
    return @{ @"type": @"array", @"array": items };
  }
  if ([value isKindOfClass:[NSDictionary class]]) {
    NSMutableDictionary *items = [NSMutableDictionary dictionaryWithCapacity:[(NSDictionary *)value count]];
    for (id key in (NSDictionary *)value) {
      if (![key isKindOfClass:[NSString class]] || [(NSString *)key length] == 0) {
        if (errorOut != NULL) {
          *errorOut = @"ImageIO metadata contains a non-string or empty dictionary key";
        }
        return nil;
      }
      NSDictionary *typed = pcTypedMetadataValue([(NSDictionary *)value objectForKey:key], errorOut);
      if (typed == nil) {
        return nil;
      }
      items[key] = typed;
    }
    return @{ @"type": @"dictionary", @"dictionary": items };
  }
  if (errorOut != NULL) {
    *errorOut = [NSString stringWithFormat:@"unsupported ImageIO metadata class %@", NSStringFromClass([value class])];
  }
  return nil;
}

static char *pcTypedMetadataFixtureJSON(char **errorOut) {
  NSString *typingError = nil;
  NSDictionary *fixture = @{
    @"string": @"synthetic text",
    @"boolean": @YES,
    @"signed": @(-2),
    @"unsigned": [NSNumber numberWithUnsignedLongLong:18446744073709551615ULL],
    @"decimal": @1.25,
    @"date": [NSDate dateWithTimeIntervalSince1970:0],
    @"binary": [NSData dataWithBytes:"synthetic bytes" length:15],
    @"array": @[@1, @"two", [NSNull null]],
    @"dictionary": @{@"nested": [NSNull null]}
  };
  NSDictionary *typed = pcTypedMetadataValue(fixture, &typingError);
  if (typed == nil) {
    pcSetError(errorOut, [NSString stringWithFormat:@"type synthetic metadata fixture: %@", pcString(typingError)]);
    return NULL;
  }
  NSError *jsonError = nil;
  NSData *data = [NSJSONSerialization dataWithJSONObject:typed options:0 error:&jsonError];
  if (data == nil) {
    pcSetError(errorOut, [NSString stringWithFormat:@"encode synthetic metadata fixture: %@", jsonError.localizedDescription]);
    return NULL;
  }
  char *json = malloc(data.length + 1);
  if (json == NULL) {
    pcSetError(errorOut, @"allocate synthetic metadata fixture JSON");
    return NULL;
  }
  memcpy(json, data.bytes, data.length);
  json[data.length] = '\0';
  return json;
}

char *photoscrawl_image_metadata_typed_fixture_json(char **errorOut) {
  @autoreleasepool {
    if (errorOut != NULL) {
      *errorOut = NULL;
    }
    return pcTypedMetadataFixtureJSON(errorOut);
  }
}

int photoscrawl_write_image_metadata_fixture(const char *destinationPath, char **errorOut) {
  @autoreleasepool {
    if (errorOut != NULL) {
      *errorOut = NULL;
    }
    NSString *destination = destinationPath == NULL ? @"" : [NSString stringWithUTF8String:destinationPath];
    if (destination.length == 0) {
      pcSetError(errorOut, @"fixture destination path is required");
      return 0;
    }
    NSURL *destinationURL = [NSURL fileURLWithPath:destination];
    if (!pcEnsureParentDirectory(destinationURL, errorOut)) {
      return 0;
    }
    CGColorSpaceRef colorSpace = CGColorSpaceCreateWithName(kCGColorSpaceSRGB);
    if (colorSpace == NULL) {
      pcSetError(errorOut, @"create synthetic fixture colour space");
      return 0;
    }
    CGContextRef context = CGBitmapContextCreate(NULL, 2, 2, 8, 0, colorSpace, kCGImageAlphaPremultipliedLast | kCGBitmapByteOrder32Big);
    CGColorSpaceRelease(colorSpace);
    if (context == NULL) {
      pcSetError(errorOut, @"create synthetic fixture bitmap");
      return 0;
    }
    CGContextSetRGBFillColor(context, 0.2, 0.4, 0.8, 1.0);
    CGContextFillRect(context, CGRectMake(0, 0, 2, 2));
    CGImageRef image = CGBitmapContextCreateImage(context);
    CGContextRelease(context);
    if (image == NULL) {
      pcSetError(errorOut, @"create synthetic fixture image");
      return 0;
    }

    NSDictionary *exif = @{
      (NSString *)kCGImagePropertyExifExposureTime: @0.008333333333333333,
      (NSString *)kCGImagePropertyExifFNumber: @1.8,
      (NSString *)kCGImagePropertyExifFocalLength: @6.86,
      (NSString *)kCGImagePropertyExifISOSpeedRatings: @[@64],
      (NSString *)kCGImagePropertyExifDateTimeOriginal: @"2026:07:10 12:34:56",
      (NSString *)kCGImagePropertyExifOffsetTimeOriginal: @"+02:00",
      (NSString *)kCGImagePropertyExifUserComment: [NSData dataWithBytes:"synthetic comment" length:17]
    };
    NSDictionary *gps = @{
      (NSString *)kCGImagePropertyGPSLatitude: @52.367612345678,
      (NSString *)kCGImagePropertyGPSLatitudeRef: @"N",
      (NSString *)kCGImagePropertyGPSLongitude: @4.904112345678,
      (NSString *)kCGImagePropertyGPSLongitudeRef: @"E",
      (NSString *)kCGImagePropertyGPSHPositioningError: @8.25,
      (NSString *)kCGImagePropertyGPSDateStamp: @"2026:07:10",
      (NSString *)kCGImagePropertyGPSTimeStamp: @[@10, @34, @56]
    };
    NSDictionary *tiff = @{
      (NSString *)kCGImagePropertyTIFFMake: @"Synthetic Camera",
      (NSString *)kCGImagePropertyTIFFModel: @"Synthetic Model",
      (NSString *)kCGImagePropertyTIFFOrientation: @6
    };
    NSDictionary *iptc = @{
      (NSString *)kCGImagePropertyIPTCCaptionAbstract: @"Synthetic caption"
    };
    NSDictionary *properties = @{
      (NSString *)kCGImagePropertyExifDictionary: exif,
      (NSString *)kCGImagePropertyGPSDictionary: gps,
      (NSString *)kCGImagePropertyTIFFDictionary: tiff,
      (NSString *)kCGImagePropertyIPTCDictionary: iptc
    };
    [[NSFileManager defaultManager] removeItemAtURL:destinationURL error:nil];
    CGImageDestinationRef imageDestination = CGImageDestinationCreateWithURL((__bridge CFURLRef)destinationURL, CFSTR("public.jpeg"), 1, NULL);
    if (imageDestination == NULL) {
      CGImageRelease(image);
      pcSetError(errorOut, @"create synthetic fixture destination");
      return 0;
    }
    CGImageDestinationAddImage(imageDestination, image, (__bridge CFDictionaryRef)properties);
    BOOL ok = CGImageDestinationFinalize(imageDestination);
    CFRelease(imageDestination);
    CGImageRelease(image);
    if (!ok) {
      pcSetError(errorOut, @"write synthetic ImageIO metadata fixture");
      return 0;
    }
    return 1;
  }
}

static PHAuthorizationStatus pcCurrentAuthorizationStatus(void) {
  if (@available(macOS 11.0, *)) {
    // macOS Photos exposes asset fetch access through ReadWrite; AddOnly cannot
    // enumerate the library. This bridge still only calls fetch/read APIs.
    return [PHPhotoLibrary authorizationStatusForAccessLevel:PHAccessLevelReadWrite];
  }

  return [PHPhotoLibrary authorizationStatus];
}

static const NSTimeInterval pcAuthorizationRequestTimeout = 4 * 60;

static PHAuthorizationStatus pcRequestAuthorization(BOOL *timedOutOut) {
  if (timedOutOut != NULL) {
    *timedOutOut = NO;
  }
  __block PHAuthorizationStatus status = pcCurrentAuthorizationStatus();
  if (status != PHAuthorizationStatusNotDetermined) {
    return status;
  }

  NSApplication *application = [NSApplication sharedApplication];
  [application setActivationPolicy:NSApplicationActivationPolicyRegular];
  [application activateIgnoringOtherApps:YES];

  __block BOOL finished = NO;
  __block BOOL timedOut = NO;
  void (^finish)(PHAuthorizationStatus, BOOL) = ^(PHAuthorizationStatus requestedStatus, BOOL didTimeOut) {
    dispatch_async(dispatch_get_main_queue(), ^{
      if (finished) {
        return;
      }
      finished = YES;
      timedOut = didTimeOut;
      if (!didTimeOut) {
        status = requestedStatus;
      }
      [application stop:nil];
    });
  };

  void (^completeRequest)(PHAuthorizationStatus) = ^(PHAuthorizationStatus requestedStatus) {
    finish(requestedStatus, NO);
  };
  NSTimer *timeout = [NSTimer scheduledTimerWithTimeInterval:pcAuthorizationRequestTimeout repeats:NO block:^(__unused NSTimer *timer) {
    finish(PHAuthorizationStatusNotDetermined, YES);
  }];
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
  [timeout invalidate];
  if (timedOutOut != NULL) {
    *timedOutOut = timedOut;
  }
  return status;
}

static BOOL pcRequireAuthorization(PHAuthorizationStatus status, char **errorOut) {
  if (status == PHAuthorizationStatusAuthorized || status == PHAuthorizationStatusLimited) {
    return YES;
  }
  pcSetError(errorOut, [NSString stringWithFormat:@"photos_access:%@", pcAuthorizationStatus(status)]);
  return NO;
}

int photoscrawl_location_is_valid(double latitude, double longitude) {
  if (!(latitude >= -90.0 && latitude <= 90.0 && longitude >= -180.0 && longitude <= 180.0)) {
    return 0;
  }
  return latitude != 0.0 || longitude != 0.0;
}

static NSDictionary *pcLocationDictionary(CLLocation *location) {
  if (location == nil || !photoscrawl_location_is_valid(location.coordinate.latitude, location.coordinate.longitude)) {
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
    BOOL timedOut = NO;
    PHAuthorizationStatus status = pcRequestAuthorization(&timedOut);
    if (timedOut) {
      pcSetError(errorOut, @"PhotoKit authorization request timed out");
      return NULL;
    }
    return pcCopyCString(pcAuthorizationStatus(status));
  }
}

char *photoscrawl_photokit_authorization_status(char **errorOut) {
  @autoreleasepool {
    if (errorOut != NULL) {
      *errorOut = NULL;
    }
    if (!@available(macOS 10.15, *)) {
      pcSetError(errorOut, @"PhotoKit authorization status requires macOS 10.15 or newer");
      return NULL;
    }
    return pcCopyCString(pcAuthorizationStatus(pcCurrentAuthorizationStatus()));
  }
}

char *photoscrawl_asset_readiness(const char *assetUUID, char **errorOut) {
  @autoreleasepool {
    if (errorOut != NULL) *errorOut = NULL;
    if (!@available(macOS 10.15, *)) {
      pcSetError(errorOut, @"PhotoKit asset readiness requires macOS 10.15 or newer");
      return NULL;
    }
    if (!pcRequireAuthorization(pcCurrentAuthorizationStatus(), errorOut)) return NULL;
    NSString *uuid = assetUUID == NULL ? @"" : [NSString stringWithUTF8String:assetUUID];
    if (pcIdentifierUUID(uuid).length == 0) {
      pcSetError(errorOut, @"PhotoKit asset readiness requires an asset UUID");
      return NULL;
    }
    PHAsset *selected = pcFetchAssetForIdentifier(uuid);
    if (selected == nil) {
      pcSetError(errorOut, @"PhotoKit asset not found");
      return NULL;
    }
    if (selected.mediaType != PHAssetMediaTypeImage) {
      pcSetError(errorOut, @"PhotoKit asset is not an image");
      return NULL;
    }
    if (pcLocationDictionary(selected.location) != nil) {
      pcSetError(errorOut, @"PhotoKit asset has a valid location");
      return NULL;
    }
    PHAssetResource *resource = pcPreferredOriginalResource(selected);
    if (resource == nil || resource.originalFilename.length == 0) {
      pcSetError(errorOut, @"PhotoKit asset has no image original resource");
      return NULL;
    }
    NSDictionary *record = @{
      @"local_identifier": pcString(selected.localIdentifier),
      @"asset_uuid": pcIdentifierUUID(selected.localIdentifier),
      @"media_type": pcMediaType(selected.mediaType),
      @"has_location": @NO,
      @"creation_date": pcDate(selected.creationDate),
      @"modification_date": pcDate(selected.modificationDate),
      @"pixel_width": @((long long)selected.pixelWidth),
      @"pixel_height": @((long long)selected.pixelHeight),
      @"original_filename": pcString(resource.originalFilename),
      @"original_uti": pcString(resource.uniformTypeIdentifier)
    };
    NSError *jsonError = nil;
    NSData *data = [NSJSONSerialization dataWithJSONObject:record options:0 error:&jsonError];
    if (data == nil) {
      pcSetError(errorOut, @"encode PhotoKit asset readiness");
      return NULL;
    }
    char *json = malloc(data.length + 1);
    if (json == NULL) {
      pcSetError(errorOut, @"allocate PhotoKit asset readiness");
      return NULL;
    }
    memcpy(json, data.bytes, data.length);
    json[data.length] = '\0';
    return json;
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

char *photoscrawl_image_metadata_record_json(const char *sourcePath, char **errorOut) {
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
    NSDictionary *containerProperties = CFBridgingRelease(CGImageSourceCopyProperties(imageSource, NULL));
    if (containerProperties == nil) {
      containerProperties = @{};
    }
    NSString *typingError = nil;
    NSDictionary *container = pcTypedMetadataValue(containerProperties, &typingError);
    if (container == nil) {
      CFRelease(imageSource);
      pcSetError(errorOut, [NSString stringWithFormat:@"type container metadata: %@", pcString(typingError)]);
      return NULL;
    }
    size_t imageCount = CGImageSourceGetCount(imageSource);
    NSMutableArray *images = [NSMutableArray arrayWithCapacity:imageCount];
    for (size_t index = 0; index < imageCount; index++) {
      NSDictionary *properties = CFBridgingRelease(CGImageSourceCopyPropertiesAtIndex(imageSource, index, NULL));
      if (properties == nil) {
        properties = @{};
      }
      typingError = nil;
      NSDictionary *typed = pcTypedMetadataValue(properties, &typingError);
      if (typed == nil) {
        CFRelease(imageSource);
        pcSetError(errorOut, [NSString stringWithFormat:@"type image metadata at index %zu: %@", index, pcString(typingError)]);
        return NULL;
      }
      [images addObject:@{ @"index": @(index), @"properties": typed }];
    }
    CFRelease(imageSource);
    NSDictionary *record = @{ @"container": container, @"images": images };
    NSError *jsonError = nil;
    NSData *data = [NSJSONSerialization dataWithJSONObject:record options:0 error:&jsonError];
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
