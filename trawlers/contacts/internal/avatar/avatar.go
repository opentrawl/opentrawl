package avatar

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

func InspectBytes(data []byte) (model.SourceAvatar, error) {
	if len(data) == 0 {
		return model.SourceAvatar{}, errors.New("avatar data is empty")
	}
	mime := sniff(data)
	sum := sha256.Sum256(data)
	return model.SourceAvatar{
		Data:   append([]byte(nil), data...),
		MIME:   mime,
		SHA256: hex.EncodeToString(sum[:]),
	}, nil
}

func sniff(data []byte) string {
	mime := http.DetectContentType(data)
	if i := strings.IndexByte(mime, ';'); i >= 0 {
		mime = mime[:i]
	}
	return mime
}
