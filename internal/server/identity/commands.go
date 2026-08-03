package identity

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"time"

	serverconfig "github.com/Ceinl/plumtree/internal/server/config"
	"github.com/Ceinl/plumtree/internal/sqlite"
)

// RunCommand is the trusted local author/audit command surface. It uses a
// private FlagSet and explicit output writer, so it cannot mutate process-wide
// argv, flags, or environment. Secret material is accepted for local use but
// is never printed.
func (s *Service) RunCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: expected author or audit", serverconfig.ErrInvalid)
	}
	switch args[0] {
	case "author":
		return s.runAuthorCommand(args[1:], out)
	case "audit":
		return s.runAuditCommand(args[1:], out)
	default:
		return fmt.Errorf("%w: unknown local command %q", serverconfig.ErrInvalid, args[0])
	}
}

func (s *Service) runAuthorCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: expected register, devices, rename, or retire", serverconfig.ErrInvalid)
	}
	switch args[0] {
	case "register":
		fs := flag.NewFlagSet("author register", flag.ContinueOnError)
		handle := fs.String("handle", "", "author handle")
		deviceName := fs.String("device-name", "local", "first device name")
		publicKey := fs.String("public-key", "", "first device public key")
		fingerprint := fs.String("fingerprint", "", "first device fingerprint")
		secret := fs.String("recovery-secret", "", "high-entropy local recovery secret")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		registration, err := s.RegisterAuthorLocal(context.Background(), RegisterInput{Handle: *handle, DeviceName: *deviceName, PublicKey: *publicKey, Fingerprint: *fingerprint, RecoverySecret: []byte(*secret)})
		if err != nil {
			return err
		}
		return writeJSON(out, map[string]any{"author": registration.Author, "device": registration.Device})
	case "devices":
		fs := flag.NewFlagSet("author devices", flag.ContinueOnError)
		authorID := fs.String("author-id", "", "author ID")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		devices, err := s.Devices(context.Background(), *authorID)
		if err != nil {
			return err
		}
		return writeJSON(out, map[string]any{"devices": devices})
	case "rename":
		fs := flag.NewFlagSet("author rename", flag.ContinueOnError)
		authorID := fs.String("author-id", "", "author ID")
		handle := fs.String("handle", "", "new author handle")
		reserve := fs.Duration("reserve", 24*time.Hour, "former-handle reservation duration")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return s.RenameAuthor(context.Background(), *authorID, *handle, time.Now().Add(*reserve))
	case "retire":
		fs := flag.NewFlagSet("author retire", flag.ContinueOnError)
		authorID := fs.String("author-id", "", "author ID")
		reserve := fs.Duration("reserve", 24*time.Hour, "former-handle reservation duration")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return s.RetireAuthorLocal(context.Background(), *authorID, time.Now().Add(*reserve))
	default:
		return fmt.Errorf("%w: unknown author command %q", serverconfig.ErrInvalid, args[0])
	}
}

func (s *Service) runAuditCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: expected list or prune", serverconfig.ErrInvalid)
	}
	switch args[0] {
	case "list":
		fs := flag.NewFlagSet("audit list", flag.ContinueOnError)
		authorID := fs.String("author-id", "", "scope author ID")
		action := fs.String("action", "", "exact action filter")
		targetKind := fs.String("target-kind", "", "exact target-kind filter")
		limit := fs.Int("limit", 100, "maximum events")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		events, err := s.ListAudit(context.Background(), sqlite.AuditFilter{ScopeAuthorID: *authorID, Action: *action, TargetKind: *targetKind, Limit: *limit})
		if err != nil {
			return err
		}
		return writeJSON(out, map[string]any{"events": events})
	case "prune":
		fs := flag.NewFlagSet("audit prune", flag.ContinueOnError)
		before := fs.String("before", "", "RFC3339 cutoff")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		cutoff, err := time.Parse(time.RFC3339, *before)
		if err != nil {
			return fmt.Errorf("%w: --before must be RFC3339", serverconfig.ErrInvalid)
		}
		removed, err := s.PruneAudit(context.Background(), cutoff)
		if err != nil {
			return err
		}
		return writeJSON(out, map[string]any{"removed": removed})
	default:
		return fmt.Errorf("%w: unknown audit command %q", serverconfig.ErrInvalid, args[0])
	}
}

func writeJSON(out io.Writer, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}
