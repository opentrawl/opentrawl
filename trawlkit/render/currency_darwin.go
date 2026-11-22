// Format exact currency amounts with the current macOS number preferences.
package render

/*
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
char *trawl_native_currency(const char *decimal, const char *currency, int signed_movement);
*/
import "C"
import (
	"unsafe"

	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
)

func FormatExactMonetaryAmount(amount *presentation.ExactMonetaryAmount) string {
	if amount == nil {
		return ""
	}
	decimal := C.CString(amount.GetExactDecimalAmount())
	currency := C.CString(amount.GetIso_4217CurrencyCode())
	defer C.free(unsafe.Pointer(decimal))
	defer C.free(unsafe.Pointer(currency))
	flag := C.int(0)
	if amount.GetMonetaryAmountDisplaySignStyle() == presentation.MonetaryAmountDisplaySignStyle_MONETARY_AMOUNT_DISPLAY_SIGN_STYLE_SIGNED_MOVEMENT {
		flag = 1
	}
	result := C.trawl_native_currency(decimal, currency, flag)
	defer C.free(unsafe.Pointer(result))
	return C.GoString(result)
}
