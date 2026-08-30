package gateway

import (
	"os"
	"path/filepath"

	"github.com/Ceinl/plumtree/internal/hostkey"
	"golang.org/x/crypto/ssh"
)

// devHostKey returns a stable host key, persisted under the user config dir so
// it does not change between runs — clients then trust it once instead of
// needing StrictHostKeyChecking=no on every connect. An existing-but-corrupt
// key file is a hard error: regenerating would break clients' TOFU pins. Falls
// back to an ephemeral key only when the config dir itself is unavailable.
func devHostKey() (ssh.Signer, error) {
	cfgDir, err := os.UserConfigDir()
	if err != nil {
		signer, _, err := hostkey.Generate("plumtree dev host key")
		return signer, err
	}
	return hostkey.LoadOrCreate(filepath.Join(cfgDir, "plumtree", "dev_host_ed25519"), "plumtree dev host key")
}
