package store

import "strconv"

const (
	MessageRefPrefix       = "telegram:msg/"
	LegacyMessageRefPrefix = "telecrawl:msg/"
)

func MessageRef(sourcePK int64) string {
	return MessageRefPrefix + strconv.FormatInt(sourcePK, 10)
}
