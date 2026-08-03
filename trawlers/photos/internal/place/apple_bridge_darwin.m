#import <Foundation/Foundation.h>
#import <CoreLocation/CoreLocation.h>
#import <MapKit/MapKit.h>
#include <stdlib.h>
#include <string.h>
#include <pthread.h>

int photoscrawl_current_thread_is_main(void) {
  return pthread_main_np();
}

static NSString *pcPlaceString(NSString *value) {
  return value == nil ? @"" : value;
}

static char *pcPlaceCopyCString(NSString *value) {
  const char *utf8 = [pcPlaceString(value) UTF8String];
  if (utf8 == NULL) {
    utf8 = "";
  }
  return strdup(utf8);
}

static void pcPlaceSetError(char **errorOut, NSString *message) {
  if (errorOut == NULL) {
    return;
  }
  *errorOut = pcPlaceCopyCString(message);
}

static void pcPlaceSetProviderError(char **errorDescriptionOut, char **errorDomainOut, long long *errorCodeOut, int *loadingThrottledOut, NSString *operation, NSError *error) {
  pcPlaceSetError(errorDescriptionOut, [NSString stringWithFormat:@"%@ failed: %@", operation, error.localizedDescription]);
  if (errorDomainOut != NULL) {
    *errorDomainOut = pcPlaceCopyCString(error.domain);
  }
  if (errorCodeOut != NULL) {
    *errorCodeOut = error.code;
  }
  if (loadingThrottledOut != NULL) {
    *loadingThrottledOut = [error.domain isEqualToString:MKErrorDomain] && error.code == MKErrorLoadingThrottled;
  }
}

static void pcPlaceSetString(NSMutableDictionary *dict, NSString *key, NSString *value) {
  if (value != nil && value.length > 0) {
    dict[key] = value;
  }
}

static NSString *pcPlaceFormattedAddress(NSDictionary *address) {
  NSMutableArray *parts = [NSMutableArray array];
  NSString *street = @"";
  NSString *subThoroughfare = address[@"sub_thoroughfare"];
  NSString *thoroughfare = address[@"thoroughfare"];
  if (subThoroughfare.length > 0 && thoroughfare.length > 0) {
    street = [NSString stringWithFormat:@"%@ %@", subThoroughfare, thoroughfare];
  } else if (thoroughfare.length > 0) {
    street = thoroughfare;
  }
  NSArray *values = @[
    address[@"name"] ?: @"",
    street,
    address[@"sub_locality"] ?: @"",
    address[@"locality"] ?: @"",
    address[@"administrative_area"] ?: @"",
    address[@"country"] ?: @""
  ];
  for (NSString *value in values) {
    if (value.length > 0 && ![parts containsObject:value]) {
      [parts addObject:value];
    }
  }
  return [parts componentsJoinedByString:@", "];
}

static NSDictionary *pcPlaceAddress(CLPlacemark *placemark, NSString *source) {
  if (placemark == nil) {
    return nil;
  }
  NSMutableDictionary *address = [NSMutableDictionary dictionary];
  pcPlaceSetString(address, @"name", placemark.name);
  pcPlaceSetString(address, @"thoroughfare", placemark.thoroughfare);
  pcPlaceSetString(address, @"sub_thoroughfare", placemark.subThoroughfare);
  pcPlaceSetString(address, @"locality", placemark.locality);
  pcPlaceSetString(address, @"sub_locality", placemark.subLocality);
  pcPlaceSetString(address, @"administrative_area", placemark.administrativeArea);
  pcPlaceSetString(address, @"sub_administrative_area", placemark.subAdministrativeArea);
  pcPlaceSetString(address, @"postal_code", placemark.postalCode);
  pcPlaceSetString(address, @"country", placemark.country);
  pcPlaceSetString(address, @"iso_country_code", placemark.ISOcountryCode);
  if (placemark.timeZone != nil) {
    pcPlaceSetString(address, @"time_zone", placemark.timeZone.name);
  }
  NSMutableArray *areas = [NSMutableArray array];
  for (NSString *area in placemark.areasOfInterest ?: @[]) {
    if (area.length > 0) {
      [areas addObject:area];
    }
  }
  if (areas.count > 0) {
    address[@"areas_of_interest"] = areas;
  }
  pcPlaceSetString(address, @"source", source);
  pcPlaceSetString(address, @"formatted", pcPlaceFormattedAddress(address));
  return address;
}

static NSDictionary *pcPlaceMapItemAddress(MKMapItem *item, NSString *source) API_AVAILABLE(macos(26.0)) {
  if (item == nil) {
    return nil;
  }
#pragma clang diagnostic push
#pragma clang diagnostic ignored "-Wdeprecated-declarations"
  NSMutableDictionary *address = [[pcPlaceAddress(item.placemark, source) mutableCopy] autorelease];
#pragma clang diagnostic pop
  if (address == nil) {
    address = [NSMutableDictionary dictionary];
  }
  MKAddressRepresentations *representations = item.addressRepresentations;
  MKAddress *mapAddress = item.address;
  pcPlaceSetString(address, @"name", item.name);
  if ([address[@"locality"] length] == 0) {
    pcPlaceSetString(address, @"locality", representations.cityName);
  }
  if ([address[@"country"] length] == 0) {
    pcPlaceSetString(address, @"country", representations.regionName);
  }
  if ([address[@"iso_country_code"] length] == 0) {
    pcPlaceSetString(address, @"iso_country_code", representations.regionCode);
  }
  NSString *formatted = mapAddress.fullAddress;
  if (formatted.length == 0) {
    formatted = [representations fullAddressIncludingRegion:YES singleLine:YES];
  }
  pcPlaceSetString(address, @"formatted", formatted);
  pcPlaceSetString(address, @"source", source);
  return address.count > 1 ? address : nil;
}

static NSDictionary *pcPlaceCoordinate(CLLocation *location) {
  if (location == nil) {
    return nil;
  }
  return @{
    @"latitude": @(location.coordinate.latitude),
    @"longitude": @(location.coordinate.longitude)
  };
}

static NSDictionary *pcPlaceCandidate(MKMapItem *item, CLLocation *origin) {
  if (item == nil) {
    return nil;
  }
  NSString *name = item.name;
  if (name == nil || name.length == 0) {
    return nil;
  }

  NSMutableDictionary *candidate = [NSMutableDictionary dictionary];
  candidate[@"name"] = name;
  pcPlaceSetString(candidate, @"category", item.pointOfInterestCategory);
  CLLocation *location = item.location;
  NSDictionary *address = pcPlaceMapItemAddress(item, @"apple_mapkit_local_search");
  if (location != nil && origin != nil) {
    candidate[@"distance_m"] = @([location distanceFromLocation:origin]);
    NSDictionary *coordinate = pcPlaceCoordinate(location);
    if (coordinate != nil) {
      candidate[@"coordinate"] = coordinate;
    }
  }
  if (address != nil) {
    candidate[@"address"] = address;
  }
  candidate[@"source"] = @"apple_mapkit_local_search";
  candidate[@"provenance"] = @[@"MKLocalPointsOfInterestRequest"];
  return candidate;
}

static BOOL pcPlaceWait(BOOL *done, NSTimeInterval timeoutSeconds) {
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

static char *pcPlaceEncodeJSON(NSDictionary *result, char **errorOut) {
  NSError *encodeError = nil;
  NSData *data = [NSJSONSerialization dataWithJSONObject:result options:0 error:&encodeError];
  if (data == nil) {
    pcPlaceSetError(errorOut, [NSString stringWithFormat:@"Apple location response could not be encoded: %@", encodeError.localizedDescription]);
    return NULL;
  }
  char *json = malloc(data.length + 1);
  if (json == NULL) {
    pcPlaceSetError(errorOut, @"Apple location response could not be allocated");
    return NULL;
  }
  memcpy(json, data.bytes, data.length);
  json[data.length] = '\0';
  return json;
}

char *photoscrawl_apple_reverse_geocoding_json(double latitude, double longitude, char **errorDescriptionOut, char **errorDomainOut, long long *errorCodeOut, int *loadingThrottledOut) {
  @autoreleasepool {
    if (errorDescriptionOut != NULL) {
      *errorDescriptionOut = NULL;
    }
    if (errorDomainOut != NULL) {
      *errorDomainOut = NULL;
    }
    if (errorCodeOut != NULL) {
      *errorCodeOut = 0;
    }
    if (loadingThrottledOut != NULL) {
      *loadingThrottledOut = 0;
    }

    CLLocation *origin = [[CLLocation alloc] initWithLatitude:latitude longitude:longitude];
    MKReverseGeocodingRequest *request = [[MKReverseGeocodingRequest alloc] initWithLocation:origin];
    if (request == nil) {
      pcPlaceSetError(errorDescriptionOut, @"Apple reverse geocoding could not create a request");
      [origin release];
      return NULL;
    }

    __block NSArray<MKMapItem *> *mapItems = nil;
    __block NSError *geocodingError = nil;
    __block BOOL geocodingDone = NO;
    __block BOOL acceptingGeocodingCompletion = YES;
    [request getMapItemsWithCompletionHandler:^(NSArray<MKMapItem *> * _Nullable found, NSError * _Nullable error) {
      if (!acceptingGeocodingCompletion) {
        return;
      }
      acceptingGeocodingCompletion = NO;
      mapItems = [found retain];
      geocodingError = [error retain];
      geocodingDone = YES;
    }];
    if (!pcPlaceWait(&geocodingDone, 20.0)) {
      acceptingGeocodingCompletion = NO;
      [request cancel];
      pcPlaceSetError(errorDescriptionOut, @"Apple reverse geocoding timed out");
      [mapItems release];
      [geocodingError release];
      [request release];
      [origin release];
      return NULL;
    }
    if (geocodingError != nil) {
      pcPlaceSetProviderError(errorDescriptionOut, errorDomainOut, errorCodeOut, loadingThrottledOut, @"Apple reverse geocoding", geocodingError);
      [mapItems release];
      [geocodingError release];
      [request release];
      [origin release];
      return NULL;
    }

    NSMutableDictionary *result = [NSMutableDictionary dictionary];
    NSMutableArray *retainedMapItems = [NSMutableArray arrayWithCapacity:mapItems.count];
    for (MKMapItem *mapItem in mapItems) {
      NSMutableDictionary *retainedMapItem = [NSMutableDictionary dictionary];
      pcPlaceSetString(retainedMapItem, @"name", mapItem.name);
      NSDictionary *coordinate = pcPlaceCoordinate(mapItem.location);
      if (coordinate != nil) {
        retainedMapItem[@"coordinate"] = coordinate;
      }
      NSDictionary *address = pcPlaceMapItemAddress(mapItem, @"apple_mapkit_reverse_geocoding");
      if (address != nil) {
        retainedMapItem[@"address"] = address;
        if (result[@"address"] == nil) {
          result[@"address"] = address;
        }
      }
      [retainedMapItems addObject:retainedMapItem];
    }
    result[@"map_items"] = retainedMapItems;
    char *encodedResult = pcPlaceEncodeJSON(result, errorDescriptionOut);
    [mapItems release];
    [geocodingError release];
    [request release];
    [origin release];
    return encodedResult;
  }
}

char *photoscrawl_apple_nearby_places_json(double latitude, double longitude, double radius, int maximumCandidates, char **errorDescriptionOut, char **errorDomainOut, long long *errorCodeOut, int *loadingThrottledOut) {
  @autoreleasepool {
    if (errorDescriptionOut != NULL) {
      *errorDescriptionOut = NULL;
    }
    if (errorDomainOut != NULL) {
      *errorDomainOut = NULL;
    }
    if (errorCodeOut != NULL) {
      *errorCodeOut = 0;
    }
    if (loadingThrottledOut != NULL) {
      *loadingThrottledOut = 0;
    }
    CLLocation *origin = [[CLLocation alloc] initWithLatitude:latitude longitude:longitude];
    __block MKLocalSearchResponse *searchResponse = nil;
    __block NSError *searchError = nil;
    __block BOOL searchDone = NO;
    __block BOOL acceptingSearchCompletion = YES;
    MKLocalPointsOfInterestRequest *nearbyRequest = [[MKLocalPointsOfInterestRequest alloc] initWithCenterCoordinate:origin.coordinate radius:radius];
    MKLocalSearch *search = [[MKLocalSearch alloc] initWithPointsOfInterestRequest:nearbyRequest];
    [search startWithCompletionHandler:^(MKLocalSearchResponse * _Nullable response, NSError * _Nullable error) {
      if (!acceptingSearchCompletion) {
        return;
      }
      acceptingSearchCompletion = NO;
      searchResponse = [response retain];
      searchError = [error retain];
      searchDone = YES;
    }];
    if (!pcPlaceWait(&searchDone, 20.0)) {
      acceptingSearchCompletion = NO;
      [search cancel];
      pcPlaceSetError(errorDescriptionOut, @"Apple nearby place search timed out");
      [searchResponse release];
      [searchError release];
      [search release];
      [nearbyRequest release];
      [origin release];
      return NULL;
    }
    NSMutableDictionary *result = [NSMutableDictionary dictionary];
    if (searchError != nil) {
      if ([searchError.domain isEqualToString:MKErrorDomain] && searchError.code == MKErrorPlacemarkNotFound) {
        result[@"candidates"] = @[];
      } else {
        pcPlaceSetProviderError(errorDescriptionOut, errorDomainOut, errorCodeOut, loadingThrottledOut, @"Apple nearby place search", searchError);
        [searchResponse release];
        [searchError release];
        [search release];
        [nearbyRequest release];
        [origin release];
        return NULL;
      }
    } else {
      NSMutableArray *candidates = [NSMutableArray array];
      for (MKMapItem *item in searchResponse.mapItems) {
        NSDictionary *candidate = pcPlaceCandidate(item, origin);
        if (candidate != nil) {
          [candidates addObject:candidate];
          if (candidates.count == maximumCandidates) {
            break;
          }
        }
      }
      result[@"candidates"] = candidates;
    }
    [searchResponse release];
    [searchError release];
    [search release];
    [nearbyRequest release];
    [origin release];
    return pcPlaceEncodeJSON(result, errorDescriptionOut);
  }
}
