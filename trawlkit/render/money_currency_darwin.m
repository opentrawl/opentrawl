// Format exact currency amounts with the current macOS number preferences.
#import <Foundation/Foundation.h>
#include <string.h>

char *money_native_currency(const char *decimal, const char *currency, int signed_movement) {
    @autoreleasepool {
        NSString *decimalText = [NSString stringWithUTF8String:decimal];
        NSDecimalNumber *amount = [NSDecimalNumber decimalNumberWithString:decimalText
                                                                  locale:@{NSLocaleDecimalSeparator: @"."}];
        NSNumberFormatter *currencyFormatter = [[NSNumberFormatter alloc] init];
        currencyFormatter.locale = NSLocale.autoupdatingCurrentLocale;
        currencyFormatter.numberStyle = NSNumberFormatterCurrencyStyle;
        currencyFormatter.currencyCode = [NSString stringWithUTF8String:currency];

        NSDictionary *numberSymbols = [[NSUserDefaults standardUserDefaults] objectForKey:@"AppleICUNumberSymbols"];
        NSString *decimalSeparator = numberSymbols[@"10"] ?: numberSymbols[@"0"];
        NSString *groupingSeparator = numberSymbols[@"17"] ?: numberSymbols[@"1"];
        NSNumberFormatter *numberFormatter = [[NSNumberFormatter alloc] init];
        numberFormatter.locale = currencyFormatter.locale;
        numberFormatter.numberStyle = NSNumberFormatterDecimalStyle;
        numberFormatter.minimumFractionDigits = currencyFormatter.minimumFractionDigits;
        numberFormatter.maximumFractionDigits = currencyFormatter.maximumFractionDigits;
        numberFormatter.groupingSize = currencyFormatter.groupingSize;
        numberFormatter.secondaryGroupingSize = currencyFormatter.secondaryGroupingSize;
        if (decimalSeparator) numberFormatter.decimalSeparator = decimalSeparator;
        if (groupingSeparator) numberFormatter.groupingSeparator = groupingSeparator;
        NSRange decimalPoint = [decimalText rangeOfString:@"."];
        if (decimalPoint.location != NSNotFound) {
            numberFormatter.maximumFractionDigits = MAX(numberFormatter.maximumFractionDigits,
                                                       decimalText.length - decimalPoint.location - 1);
        }

        BOOL negative = [amount compare:NSDecimalNumber.zero] == NSOrderedAscending;
        NSString *prefix = negative ? currencyFormatter.negativePrefix : currencyFormatter.positivePrefix;
        NSString *suffix = negative ? currencyFormatter.negativeSuffix : currencyFormatter.positiveSuffix;
        prefix = [prefix stringByReplacingOccurrencesOfString:@"-" withString:@"−"];
        suffix = [suffix stringByReplacingOccurrencesOfString:@"-" withString:@"−"];
        if (signed_movement && [amount compare:NSDecimalNumber.zero] == NSOrderedDescending) {
            prefix = [@"+" stringByAppendingString:prefix];
        }
        NSDecimalNumber *magnitude = negative
            ? [amount decimalNumberByMultiplyingBy:[NSDecimalNumber decimalNumberWithString:@"-1"]]
            : amount;
        NSString *rendered = [NSString stringWithFormat:@"%@%@%@", prefix,
                                                       [numberFormatter stringFromNumber:magnitude], suffix];
        char *result = strdup(rendered.UTF8String);
        [numberFormatter release];
        [currencyFormatter release];
        return result;
    }
}
