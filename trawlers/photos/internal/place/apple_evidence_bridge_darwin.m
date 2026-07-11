#import <Foundation/Foundation.h>
#import <CoreLocation/CoreLocation.h>
#import <MapKit/MapKit.h>
#include <stdlib.h>
#include <string.h>

static NSString *pcEvidenceString(NSString *value) {
  return value == nil ? @"" : value;
}

static char *pcEvidenceCopyCString(NSString *value) {
  const char *utf8 = [pcEvidenceString(value) UTF8String];
  if (utf8 == NULL) {
    utf8 = "";
  }
  return strdup(utf8);
}

static void pcEvidenceSetError(char **errorOut, NSString *message) {
  if (errorOut != NULL) {
    *errorOut = pcEvidenceCopyCString(message);
  }
}

static void pcEvidenceSetString(NSMutableDictionary *dict, NSString *key, NSString *value) {
  if (value != nil && value.length > 0) {
    dict[key] = value;
  }
}

static BOOL pcEvidenceWait(BOOL *done, NSTimeInterval timeoutSeconds) {
  NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:timeoutSeconds];
  NSRunLoop *runLoop = [NSRunLoop currentRunLoop];
  while (!*done && [deadline timeIntervalSinceNow] > 0) {
    BOOL processed = [runLoop runMode:NSDefaultRunLoopMode beforeDate:[NSDate dateWithTimeIntervalSinceNow:0.05]];
    if (!processed && !*done) {
      [NSThread sleepForTimeInterval:0.02];
    }
  }
  return *done;
}

#if __MAC_OS_X_VERSION_MAX_ALLOWED >= 260000
static NSDictionary *pcEvidenceCoordinate(CLLocation *location) API_AVAILABLE(macos(26.0)) {
  if (location == nil) {
    return nil;
  }
  return @{
    @"latitude": @(location.coordinate.latitude),
    @"longitude": @(location.coordinate.longitude)
  };
}

static NSDictionary *pcEvidenceAddress(MKMapItem *item, NSString *source) API_AVAILABLE(macos(26.0)) {
  if (item == nil) {
    return nil;
  }
  NSMutableDictionary *address = [NSMutableDictionary dictionary];
  MKAddressRepresentations *representations = item.addressRepresentations;
  MKAddress *mapAddress = item.address;
  pcEvidenceSetString(address, @"name", item.name);
  pcEvidenceSetString(address, @"locality", representations.cityName);
  pcEvidenceSetString(address, @"country", representations.regionName);
  pcEvidenceSetString(address, @"iso_country_code", representations.regionCode);
  NSString *formatted = mapAddress.fullAddress;
  if (formatted.length == 0) {
    formatted = [representations fullAddressIncludingRegion:YES singleLine:YES];
  }
  pcEvidenceSetString(address, @"formatted", formatted);
  pcEvidenceSetString(address, @"source", source);
  return address.count > 1 ? address : nil;
}

static NSDictionary *pcEvidenceMapItem(MKMapItem *item, CLLocation *origin, NSString *source) API_AVAILABLE(macos(26.0)) {
  NSMutableDictionary *candidate = [NSMutableDictionary dictionary];
  pcEvidenceSetString(candidate, @"name", item.name);
  pcEvidenceSetString(candidate, @"category", item.pointOfInterestCategory);
  CLLocation *location = item.location;
  if (location != nil) {
    NSDictionary *coordinate = pcEvidenceCoordinate(location);
    if (coordinate != nil) {
      candidate[@"coordinate"] = coordinate;
    }
    if (origin != nil) {
      candidate[@"distance_m"] = @([location distanceFromLocation:origin]);
    }
  }
  NSDictionary *address = pcEvidenceAddress(item, source);
  if (address != nil) {
    candidate[@"address"] = address;
  }
  candidate[@"source"] = source;
  return candidate;
}
#endif

char *photoscrawl_place_evidence_json(const char *requestJSON, char **errorOut) {
  @autoreleasepool {
    if (errorOut != NULL) {
      *errorOut = NULL;
    }
    NSString *requestString = requestJSON == NULL ? @"" : [NSString stringWithUTF8String:requestJSON];
    NSData *requestData = [requestString dataUsingEncoding:NSUTF8StringEncoding];
    NSError *jsonError = nil;
    NSDictionary *requestValues = requestData == nil ? nil : [NSJSONSerialization JSONObjectWithData:requestData options:0 error:&jsonError];
    if (![requestValues isKindOfClass:[NSDictionary class]]) {
      pcEvidenceSetError(errorOut, [NSString stringWithFormat:@"decode Apple evidence request: %@", jsonError.localizedDescription]);
      return NULL;
    }

#if __MAC_OS_X_VERSION_MAX_ALLOWED >= 260000
    if (@available(macOS 26.0, *)) {
      double latitude = [requestValues[@"latitude"] doubleValue];
      double longitude = [requestValues[@"longitude"] doubleValue];
      double radius = [requestValues[@"radius_meters"] doubleValue];
      CLLocation *origin = [[CLLocation alloc] initWithLatitude:latitude longitude:longitude];

      __block NSArray<MKMapItem *> *reverseItems = nil;
      __block NSError *reverseError = nil;
      __block BOOL reverseDone = NO;
      MKReverseGeocodingRequest *reverseRequest = [[MKReverseGeocodingRequest alloc] initWithLocation:origin];
      [reverseRequest getMapItemsWithCompletionHandler:^(NSArray<MKMapItem *> * _Nullable found, NSError * _Nullable error) {
        reverseItems = [found retain];
        reverseError = [error retain];
        reverseDone = YES;
      }];
      if (!pcEvidenceWait(&reverseDone, 20.0)) {
        [reverseRequest cancel];
        pcEvidenceSetError(errorOut, @"Apple evidence reverse request timed out");
        [reverseItems release];
        [reverseError release];
        [reverseRequest release];
        [origin release];
        return NULL;
      }
      if (reverseError != nil) {
        pcEvidenceSetError(errorOut, [NSString stringWithFormat:@"Apple evidence reverse request failed: %@", reverseError.localizedDescription]);
        [reverseItems release];
        [reverseError release];
        [reverseRequest release];
        [origin release];
        return NULL;
      }

      __block MKLocalSearchResponse *nearbyResponse = nil;
      __block NSError *nearbyError = nil;
      __block BOOL nearbyDone = NO;
      MKLocalPointsOfInterestRequest *nearbyRequest = [[MKLocalPointsOfInterestRequest alloc] initWithCenterCoordinate:origin.coordinate radius:radius];
      MKLocalSearch *nearbySearch = [[MKLocalSearch alloc] initWithPointsOfInterestRequest:nearbyRequest];
      [nearbySearch startWithCompletionHandler:^(MKLocalSearchResponse * _Nullable response, NSError * _Nullable error) {
        nearbyResponse = [response retain];
        nearbyError = [error retain];
        nearbyDone = YES;
      }];
      if (!pcEvidenceWait(&nearbyDone, 20.0)) {
        [nearbySearch cancel];
        pcEvidenceSetError(errorOut, @"Apple evidence nearby request timed out");
        [reverseItems release];
        [reverseError release];
        [reverseRequest release];
        [nearbyResponse release];
        [nearbyError release];
        [nearbySearch release];
        [nearbyRequest release];
        [origin release];
        return NULL;
      }
      if (nearbyError != nil) {
        pcEvidenceSetError(errorOut, [NSString stringWithFormat:@"Apple evidence nearby request failed: %@", nearbyError.localizedDescription]);
        [reverseItems release];
        [reverseError release];
        [reverseRequest release];
        [nearbyResponse release];
        [nearbyError release];
        [nearbySearch release];
        [nearbyRequest release];
        [origin release];
        return NULL;
      }

      NSMutableArray *reverseEvidence = [NSMutableArray arrayWithCapacity:reverseItems.count];
      for (MKMapItem *item in reverseItems ?: @[]) {
        [reverseEvidence addObject:pcEvidenceMapItem(item, origin, @"apple_mapkit_reverse")];
      }
      NSMutableArray *nearbyEvidence = [NSMutableArray arrayWithCapacity:nearbyResponse.mapItems.count];
      for (MKMapItem *item in nearbyResponse.mapItems ?: @[]) {
        [nearbyEvidence addObject:pcEvidenceMapItem(item, origin, @"apple_mapkit_local_search")];
      }
      NSDictionary *result = @{
        @"reverse_items": reverseEvidence,
        @"nearby_items": nearbyEvidence
      };
      NSError *encodeError = nil;
      NSData *data = [NSJSONSerialization dataWithJSONObject:result options:0 error:&encodeError];

      [reverseItems release];
      [reverseError release];
      [reverseRequest release];
      [nearbyResponse release];
      [nearbyError release];
      [nearbySearch release];
      [nearbyRequest release];
      [origin release];

      if (data == nil) {
        pcEvidenceSetError(errorOut, [NSString stringWithFormat:@"encode Apple evidence result: %@", encodeError.localizedDescription]);
        return NULL;
      }
      char *json = malloc(data.length + 1);
      if (json == NULL) {
        pcEvidenceSetError(errorOut, @"allocate Apple evidence JSON");
        return NULL;
      }
      memcpy(json, data.bytes, data.length);
      json[data.length] = '\0';
      return json;
    }
#endif

    pcEvidenceSetError(errorOut, @"Apple place evidence requires macOS 26 or newer");
    return NULL;
  }
}
