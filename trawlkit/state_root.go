package trawlkit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opentrawl/opentrawl/trawlkit/config"
)

const StateRootEnvironment = "OPENTRAWL_STATE_ROOT"

// ResolveStateRoot returns the one root for OpenTrawl-owned archives,
// configuration and logs. Source databases continue to resolve from the real
// user home independently.
func ResolveStateRoot(explicit string) (string, error) {
	root := strings.TrimSpace(explicit)
	if root == "" {
		if configured, exists := os.LookupEnv(StateRootEnvironment); exists {
			root = strings.TrimSpace(configured)
			if root == "" {
				return "", fmt.Errorf("%s must not be empty", StateRootEnvironment)
			}
		} else {
			home, err := os.UserHomeDir()
			if err != nil || strings.TrimSpace(home) == "" {
				return "", fmt.Errorf("resolve home for OpenTrawl state: %w", err)
			}
			root = filepath.Join(home, ".opentrawl")
		}
	}
	root = filepath.Clean(config.ExpandHome(root))
	if !filepath.IsAbs(root) {
		return "", fmt.Errorf("%s must be an absolute path", StateRootEnvironment)
	}
	return root, nil
}
