#import <Foundation/Foundation.h>
#import <Photos/Photos.h>
#import <CoreLocation/CoreLocation.h>
#import <dispatch/dispatch.h>
#include <stdlib.h>
#include <string.h>

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

static PHAuthorizationStatus pcEnsureAuthorized(void) {
  __block PHAuthorizationStatus status;
  if (@available(macOS 11.0, *)) {
    status = [PHPhotoLibrary authorizationStatusForAccessLevel:PHAccessLevelReadWrite];
    if (status == PHAuthorizationStatusNotDetermined) {
      dispatch_semaphore_t semaphore = dispatch_semaphore_create(0);
      [PHPhotoLibrary requestAuthorizationForAccessLevel:PHAccessLevelReadWrite handler:^(PHAuthorizationStatus requestedStatus) {
        status = requestedStatus;
        dispatch_semaphore_signal(semaphore);
      }];
      dispatch_semaphore_wait(semaphore, DISPATCH_TIME_FOREVER);
    }
    return status;
  }

  status = [PHPhotoLibrary authorizationStatus];
  if (status == PHAuthorizationStatusNotDetermined) {
    dispatch_semaphore_t semaphore = dispatch_semaphore_create(0);
    [PHPhotoLibrary requestAuthorization:^(PHAuthorizationStatus requestedStatus) {
      status = requestedStatus;
      dispatch_semaphore_signal(semaphore);
    }];
    dispatch_semaphore_wait(semaphore, DISPATCH_TIME_FOREVER);
  }
  return status;
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

char *photoscrawl_photokit_snapshot(const char *libraryPath, char **errorOut) {
  @autoreleasepool {
    if (errorOut != NULL) {
      *errorOut = NULL;
    }
    if (!@available(macOS 10.15, *)) {
      pcSetError(errorOut, @"PhotoKit crawl requires macOS 10.15 or newer");
      return NULL;
    }

    NSString *path = libraryPath == NULL ? @"" : [NSString stringWithUTF8String:libraryPath];
    BOOL isDirectory = NO;
    if (path.length == 0 || ![[NSFileManager defaultManager] fileExistsAtPath:path isDirectory:&isDirectory] || !isDirectory) {
      pcSetError(errorOut, [NSString stringWithFormat:@"Photos library path does not exist or is not a directory: %@", path]);
      return NULL;
    }

    PHAuthorizationStatus status = pcEnsureAuthorized();
    if (status != PHAuthorizationStatusAuthorized && status != PHAuthorizationStatusLimited) {
      pcSetError(errorOut, [NSString stringWithFormat:@"Photos access is %@ for this process", pcAuthorizationStatus(status)]);
      return NULL;
    }

    PHFetchOptions *options = [[PHFetchOptions alloc] init];
    options.includeHiddenAssets = YES;
    options.wantsIncrementalChangeDetails = NO;
    if (@available(macOS 10.15, *)) {
      options.includeAllBurstAssets = YES;
    }
    options.sortDescriptors = @[
      [NSSortDescriptor sortDescriptorWithKey:@"creationDate" ascending:YES],
      [NSSortDescriptor sortDescriptorWithKey:@"localIdentifier" ascending:YES]
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
