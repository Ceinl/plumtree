package workflow

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/Ceinl/plumtree/internal/cli/paired"
	protocol "github.com/Ceinl/plumtree/internal/protocol/pairing"
)

func (r Runner) manager() paired.Manager {
	return paired.Manager{StorePath: r.StorePath, Keys: paired.FileKeyStore{Dir: r.KeyDir}}
}

func (r Runner) pairServer(args []string, requestedPurpose protocol.Purpose) error {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		args = append(append([]string(nil), args[1:]...), args[0])
	}
	fs := flag.NewFlagSet("pt pair", flag.ContinueOnError)
	name := fs.String("name", "", "local server name")
	port := fs.Int("port", 0, "explicit SSH port")
	deviceName := fs.String("device", "device", "device name")
	bootstrapID := fs.String("bootstrap", "", "local bootstrap authority ID")
	tokenID := fs.String("token", "", "device invitation ID")
	author := fs.String("author", "", "author handle for recovery")
	secretFlag := fs.String("secret", "", "one-use or current recovery phrase")
	recoveryFlag := fs.String("next-recovery-secret", "", "next offline recovery phrase")
	yes := fs.Bool("yes", false, "confirm the displayed SSH host key")
	jsonOut := fs.Bool("json", false, "emit stable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: pt pair [--bootstrap ID|--token ID] [--secret PHRASE] [--yes] HOST")
	}
	purpose, identifier := requestedPurpose, ""
	if requestedPurpose == protocol.PurposeOfflineRecovery {
		identifier = *author
		if identifier == "" {
			return errors.New("pt recover requires --author HANDLE")
		}
	} else {
		switch {
		case *bootstrapID != "" && *tokenID == "":
			identifier = *bootstrapID
		case *tokenID != "" && *bootstrapID == "":
			purpose, identifier = protocol.PurposeAddDevice, *tokenID
		default:
			return errors.New("pt pair requires exactly one of --bootstrap ID or --token ID")
		}
	}
	secret := []byte(*secretFlag)
	if len(secret) == 0 {
		value, err := readSecret(r.In)
		if err != nil {
			return fmt.Errorf("read pairing phrase: %w", err)
		}
		secret = []byte(value)
	}
	var recoverySecret []byte
	if purpose != protocol.PurposeAddDevice {
		recoverySecret = []byte(*recoveryFlag)
		if len(recoverySecret) == 0 {
			var err error
			recoverySecret, err = generatePhrase()
			if err != nil {
				return err
			}
		}
	}
	confirmed := *yes
	if !confirmed {
		confirmed = r.confirm("Trust the displayed Plumtree SSH host key?")
	}
	if !confirmed {
		return ErrConfirm
	}
	host, explicitPort, err := pairingEndpoint(fs.Arg(0), *port)
	if err != nil {
		return err
	}
	probe := r.Probe
	if probe == nil {
		probe = paired.NewProbe(5 * time.Second)
	}
	exchange := r.Pair
	if exchange == nil {
		exchange = paired.LiveExchange
	}
	input := paired.PairInput{Host: host, Port: explicitPort, Name: *name, DeviceName: *deviceName, ConfirmHostKey: true,
		Purpose: purpose, Identifier: identifier, Secret: secret, RecoverySecret: recoverySecret}
	manager := r.manager()
	var record paired.ServerRecord
	if purpose == protocol.PurposeOfflineRecovery {
		record, err = manager.Recover(context.Background(), input, probe, exchange)
	} else {
		record, err = manager.Pair(context.Background(), input, probe, exchange)
	}
	for i := range secret {
		secret[i] = 0
	}
	if err != nil {
		return err
	}
	_, out, _ := r.streams()
	if *jsonOut {
		value := map[string]any{"server": record.Redacted()}
		if len(recoverySecret) > 0 {
			value["recoverySecret"] = string(recoverySecret)
		}
		return writeStable(out, value)
	}
	_, _ = fmt.Fprintf(out, "Paired %s as %s (%s)\n", record.Name, record.AuthorHandle, record.DeviceName)
	if len(recoverySecret) > 0 {
		_, _ = fmt.Fprintf(out, "Save this recovery phrase now; it is not stored by pt:\n%s\n", recoverySecret)
	}
	return nil
}

func (r Runner) server(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: pt server list|current|use|rename|unpair|forget")
	}
	manager := r.manager()
	_, out, _ := r.streams()
	switch args[0] {
	case "pair":
		return r.pairServer(args[1:], protocol.PurposeNewAuthor)
	case "recover":
		return r.pairServer(args[1:], protocol.PurposeOfflineRecovery)
	case "list":
		if len(args) != 1 {
			return errors.New("usage: pt server list")
		}
		store, err := paired.Load(r.StorePath)
		if err != nil {
			return err
		}
		servers := make([]paired.RedactedRecord, 0, len(store.Servers))
		for _, record := range store.Servers {
			servers = append(servers, record.Redacted())
		}
		return writeStable(out, map[string]any{"current": store.Current, "servers": servers})
	case "current":
		record, err := manager.Current()
		if err != nil {
			return err
		}
		return writeStable(out, record.Redacted())
	case "use":
		if len(args) != 2 {
			return errors.New("usage: pt server use NAME")
		}
		return manager.Switch(args[1])
	case "rename":
		if len(args) != 3 {
			return errors.New("usage: pt server rename OLD NEW")
		}
		return manager.Rename(args[1], args[2])
	case "unpair":
		return r.removeServer(args[1:], false)
	case "forget":
		return r.removeServer(args[1:], true)
	default:
		return fmt.Errorf("unknown server command %q", args[0])
	}
}

func (r Runner) removeServer(args []string, forget bool) error {
	yes, targets, err := yesAndTargets(args)
	if err != nil || len(targets) > 1 {
		return errors.New("usage: pt server unpair|forget [NAME] [--yes]")
	}
	manager := r.manager()
	name := ""
	if len(targets) == 1 {
		name = targets[0]
	} else {
		record, err := manager.Current()
		if err != nil {
			return err
		}
		name = record.Name
	}
	if !yes && !r.confirm("Remove this paired server?") {
		return ErrConfirm
	}
	if forget {
		return manager.Forget(name)
	}
	return manager.Unpair(context.Background(), name, func(ctx context.Context, record paired.ServerRecord) error {
		if r.Open == nil {
			return errors.New("clean pt API opener is not configured")
		}
		api, err := r.Open(ctx, record)
		if err != nil {
			return err
		}
		defer api.Close.Close()
		return api.RevokeDevice(ctx, record.DeviceID)
	})
}

func (r Runner) device(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: pt device list|invite|revoke")
	}
	api, _, err := r.openTarget(context.Background(), "")
	if err != nil {
		return err
	}
	defer api.Close.Close()
	_, out, _ := r.streams()
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New("usage: pt device list")
		}
		value, err := api.Devices(context.Background())
		if err != nil {
			return err
		}
		return writeStable(out, value)
	case "invite":
		if len(args) != 2 {
			return errors.New("usage: pt device invite NAME")
		}
		invitation, err := api.InviteDevice(context.Background(), args[1])
		if err != nil {
			return err
		}
		return writeStable(out, map[string]any{"invitation": invitation})
	case "revoke":
		yes, targets, err := yesAndTargets(args[1:])
		if err != nil || len(targets) != 1 {
			return errors.New("usage: pt device revoke DEVICE_ID [--yes]")
		}
		if !yes && !r.confirm("Revoke this device?") {
			return ErrConfirm
		}
		return api.RevokeDevice(context.Background(), targets[0])
	default:
		return fmt.Errorf("unknown device command %q", args[0])
	}
}

func yesAndTargets(args []string) (bool, []string, error) {
	yes := false
	var targets []string
	for _, arg := range args {
		switch arg {
		case "--yes":
			yes = true
		default:
			if strings.HasPrefix(arg, "-") {
				return false, nil, fmt.Errorf("unknown flag %s", arg)
			}
			targets = append(targets, arg)
		}
	}
	return yes, targets, nil
}

func pairingEndpoint(value string, port int) (string, int, error) {
	if host, text, err := net.SplitHostPort(value); err == nil {
		parsed, err := strconv.Atoi(text)
		if err != nil || parsed < 1 || parsed > 65535 || port != 0 {
			return "", 0, errors.New("invalid or duplicate pairing port")
		}
		return host, parsed, nil
	}
	if value == "" || port < 0 || port > 65535 {
		return "", 0, errors.New("invalid pairing endpoint")
	}
	return value, port, nil
}

func generatePhrase() ([]byte, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return nil, err
	}
	return []byte(base64.RawURLEncoding.EncodeToString(value)), nil
}
