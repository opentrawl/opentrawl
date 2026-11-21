#import <AppKit/AppKit.h>

int opentrawl_source_app_is_running(const char *bundle_identifier) {
    @autoreleasepool {
        if (bundle_identifier == NULL || bundle_identifier[0] == '\0') {
            return 0;
        }
        NSString *identifier = [NSString stringWithUTF8String:bundle_identifier];
        if (identifier == nil || identifier.length == 0) {
            return 0;
        }
        NSArray<NSRunningApplication *> *runningApplications =
            [NSRunningApplication runningApplicationsWithBundleIdentifier:identifier];
        for (NSRunningApplication *runningApplication in runningApplications) {
            if (!runningApplication.isTerminated) {
                return 1;
            }
        }
        return 0;
    }
}
