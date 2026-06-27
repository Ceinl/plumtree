// Package gatewaybackend adapts the control-plane store to the SSH gateway's
// Backend port, so the gateway can run embedded in the control plane (this
// adapter) or as its own deployable (an HTTP client) against the same store.
package gatewaybackend

import (
	"errors"
	"fmt"

	"github.com/Ceinl/plumtree/control-plane/internal/control"
	"github.com/Ceinl/plumtree/ssh-gateway/gateway"
)

// StoreBackend implements gateway.Backend directly against a *control.Store.
type StoreBackend struct {
	Store *control.Store
}

// New returns a gateway.Backend backed by store.
func New(store *control.Store) gateway.Backend { return StoreBackend{Store: store} }

func (b StoreBackend) ResolveRunnable(handle string) (gateway.Runnable, error) {
	app, deploy, artifact, wasm, err := b.Store.ResolveRunnable(handle)
	if err != nil {
		return gateway.Runnable{}, mapErr(err)
	}
	appType := artifact.BuildMetadata["app_type"]
	if appType == "" {
		appType = "tui"
	}
	return gateway.Runnable{
		AppID:    app.ID,
		AppName:  app.Name,
		OwnerID:  app.OwnerID,
		DeployID: deploy.ID,
		AppType:  appType,
		WASM:     wasm,
	}, nil
}

func (b StoreBackend) StartSession(appID, deployID string) (string, error) {
	session, err := b.Store.StartSession(appID, deployID)
	if err != nil {
		return "", mapErr(err)
	}
	return session.ID, nil
}

func (b StoreBackend) RecordSessionLog(sessionID, log string, truncated bool) error {
	_, err := b.Store.RecordSessionLog(sessionID, log, truncated)
	return err
}

func (b StoreBackend) EndSession(sessionID string) error {
	_, err := b.Store.EndSession(sessionID)
	return err
}

func (b StoreBackend) SecretsForApp(appID string) map[string]string {
	return b.Store.SecretsForApp(appID)
}

func (b StoreBackend) EgressAllowlist(appID string) []string {
	return b.Store.EgressAllowlist(appID)
}

// mapErr translates the store's sentinel errors into the gateway's so the
// gateway's errors.Is checks fire regardless of which backend produced them,
// while preserving the original error's message for logging.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, control.ErrSuspended):
		return fmt.Errorf("%w: %v", gateway.ErrSuspended, err)
	case errors.Is(err, control.ErrQuota):
		return fmt.Errorf("%w: %v", gateway.ErrQuota, err)
	default:
		return err
	}
}
