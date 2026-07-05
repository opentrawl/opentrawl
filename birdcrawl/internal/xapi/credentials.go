package xapi

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/birdcrawl/internal/tomlfile"
)

var ErrCredentialsMissing = errors.New("credentials file is missing")
var ErrCredentialsIncomplete = errors.New("credentials file is missing required OAuth keys")
var ErrCredentialsPermissions = errors.New("credentials file permissions must be 0600")

type Credentials struct {
	ClientID     string
	ClientSecret string
	AccessToken  string
	RefreshToken string
	BearerToken  string
	TokenScopes  string

	path string
	file *tomlfile.File
}

func DefaultCredentialsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".opentrawl", "birdcrawl", "credentials.toml")
	}
	return filepath.Join(home, ".opentrawl", "birdcrawl", "credentials.toml")
}

func CredentialsPresent(path string) bool {
	creds, err := LoadCredentials(path)
	return err == nil && creds.Ready()
}

func LoadCredentials(path string) (*Credentials, error) {
	if strings.TrimSpace(path) == "" {
		path = DefaultCredentialsPath()
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrCredentialsMissing
		}
		return nil, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, ErrCredentialsPermissions
	}
	file, err := tomlfile.Read(path)
	if err != nil {
		return nil, err
	}
	return &Credentials{
		ClientID:     file.Get("client_id"),
		ClientSecret: file.Get("client_secret"),
		AccessToken:  file.Get("access_token"),
		RefreshToken: file.Get("refresh_token"),
		BearerToken:  file.Get("bearer_token"),
		TokenScopes:  file.Get("token_scopes"),
		path:         path,
		file:         file,
	}, nil
}

func (c *Credentials) Ready() bool {
	return strings.TrimSpace(c.ClientID) != "" &&
		strings.TrimSpace(c.ClientSecret) != "" &&
		strings.TrimSpace(c.AccessToken) != "" &&
		strings.TrimSpace(c.RefreshToken) != ""
}

func (c *Credentials) PersistRotatedTokens(accessToken, refreshToken string) error {
	if strings.TrimSpace(accessToken) == "" || strings.TrimSpace(refreshToken) == "" {
		return errors.New("refresh response did not include required tokens")
	}
	c.AccessToken = accessToken
	c.RefreshToken = refreshToken
	c.file.Set("access_token", accessToken)
	c.file.Set("refresh_token", refreshToken)
	if err := c.file.WriteAtomic(c.path, 0o600); err != nil {
		return fmt.Errorf("persist rotated credentials: %w", err)
	}
	return nil
}
