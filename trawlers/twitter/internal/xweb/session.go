package xweb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/twitter/internal/tomlfile"
)

var (
	ErrWebSessionMissing     = errors.New("web session file is missing")
	ErrWebSessionIncomplete  = errors.New("web session file is missing auth_token or ct0")
	ErrWebSessionPermissions = errors.New("web session file permissions must be 0600")
	ErrWebSessionInvalid     = errors.New("web session cookies are invalid")
)

// WebSessionCredentials holds the browser session cookies used for read-only
// X web requests. Cookies never appear in logs, errors or output.
type WebSessionCredentials struct {
	AuthToken string
	CT0       string
	Path      string
}

func DefaultWebSessionPath(configPath string) string {
	if trimmed := strings.TrimSpace(configPath); trimmed != "" {
		return filepath.Join(filepath.Dir(trimmed), "session.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".opentrawl", "twitter", "session.toml")
	}
	return filepath.Join(home, ".opentrawl", "twitter", "session.toml")
}

func WebSessionPresent(path string) bool {
	creds, err := LoadWebSession(path)
	return err == nil && creds.Ready()
}

func LoadWebSession(path string) (*WebSessionCredentials, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultWebSessionPath("")
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrWebSessionMissing
		}
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, ErrWebSessionPermissions
	}
	file, err := tomlfile.Read(path)
	if err != nil {
		return nil, err
	}
	creds := &WebSessionCredentials{
		AuthToken: strings.TrimSpace(file.Get("auth_token")),
		CT0:       strings.TrimSpace(file.Get("ct0")),
		Path:      path,
	}
	if !creds.Ready() {
		return nil, ErrWebSessionIncomplete
	}
	if !validSessionCookieValue(creds.AuthToken) || !validSessionCookieValue(creds.CT0) {
		return nil, ErrWebSessionInvalid
	}
	return creds, nil
}

func SaveWebSession(path, authToken, ct0 string) error {
	authToken = strings.TrimSpace(authToken)
	ct0 = strings.TrimSpace(ct0)
	if !validSessionCookieValue(authToken) || !validSessionCookieValue(ct0) {
		return ErrWebSessionInvalid
	}
	if strings.TrimSpace(path) == "" {
		path = DefaultWebSessionPath("")
	}
	file := tomlfile.Empty()
	file.Set("auth_token", authToken)
	file.Set("ct0", ct0)
	if err := file.WriteAtomic(path, 0o600); err != nil {
		return fmt.Errorf("write X web session: %w", err)
	}
	return nil
}

func DeleteWebSession(path string) error {
	if strings.TrimSpace(path) == "" {
		path = DefaultWebSessionPath("")
	}
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (c *WebSessionCredentials) Ready() bool {
	return c != nil && strings.TrimSpace(c.AuthToken) != "" && strings.TrimSpace(c.CT0) != ""
}

func (c *WebSessionCredentials) cookieHeader() string {
	return "auth_token=" + c.AuthToken + "; ct0=" + c.CT0
}

func validSessionCookieValue(value string) bool {
	if len(value) < 20 || len(value) > 2048 {
		return false
	}
	for i := 0; i < len(value); i++ {
		ch := value[i]
		isAlphaNum := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
		isSafeSymbol := ch == '.' || ch == '_' || ch == '~' || ch == '%' || ch == '+' || ch == '-'
		if !isAlphaNum && !isSafeSymbol {
			return false
		}
	}
	return true
}
