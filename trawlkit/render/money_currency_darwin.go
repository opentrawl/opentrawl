// Format exact currency amounts with the current macOS number preferences.
package render

/*
#cgo LDFLAGS: -framework Foundation
#include <stdlib.h>
char *money_native_currency(const char *decimal, const char *currency, int signed_movement);
*/
import "C"
import (
	"unsafe"

	money "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/money"
)

func FormatMoneyAmount(amount *money.ExactMonetaryAmount, signed bool) string {
	if amount == nil {
		return ""
	}
	decimal := C.CString(amount.GetExactDecimalAmount())
	currency := C.CString(amount.GetIso_4217CurrencyCode())
	defer C.free(unsafe.Pointer(decimal))
	defer C.free(unsafe.Pointer(currency))
	flag := C.int(0)
	if signed {
		flag = 1
	}
	result := C.money_native_currency(decimal, currency, flag)
	defer C.free(unsafe.Pointer(result))
	return C.GoString(result)
}
