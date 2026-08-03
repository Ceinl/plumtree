package config

import (
	"context"
	"fmt"
	"time"
)

type RoleName string

const (
	RoleControl RoleName = "control"
	RoleGateway RoleName = "gateway"
	RoleRunner  RoleName = "runner"
)

type RoleProjection struct {
	name      RoleName
	config    Config
	readiness []string
}

func NewControlRole(c Config) (RoleProjection, error) {
	return newRole(RoleControl, c, []string{"storage", "control"})
}
func NewGatewayRole(c Config) (RoleProjection, error) {
	return newRole(RoleGateway, c, []string{"runner", "gateway"})
}
func NewRunnerRole(c Config) (RoleProjection, error) {
	return newRole(RoleRunner, c, []string{"runner"})
}

func newRole(name RoleName, c Config, readiness []string) (RoleProjection, error) {
	if err := c.Validate(); err != nil {
		return RoleProjection{}, err
	}
	switch name {
	case RoleControl:
		if !c.Roles.Control {
			return RoleProjection{}, fmt.Errorf("%w: control role disabled", ErrInvalid)
		}
	case RoleGateway:
		if !c.Roles.Gateway {
			return RoleProjection{}, fmt.Errorf("%w: gateway role disabled", ErrInvalid)
		}
	case RoleRunner:
		if !c.Roles.Runner {
			return RoleProjection{}, fmt.Errorf("%w: runner role disabled", ErrInvalid)
		}
	default:
		return RoleProjection{}, fmt.Errorf("%w: role", ErrInvalid)
	}
	return RoleProjection{name: name, config: c, readiness: append([]string(nil), readiness...)}, nil
}

func (r RoleProjection) Name() RoleName      { return r.name }
func (r RoleProjection) Config() Config      { return r.config }
func (r RoleProjection) Readiness() []string { return append([]string(nil), r.readiness...) }

type Composition struct{ control, gateway, runner RoleProjection }

func Compose(c Config) (Composition, error) {
	if err := c.Validate(); err != nil {
		return Composition{}, err
	}
	if !c.Roles.Control || !c.Roles.Gateway || !c.Roles.Runner {
		return Composition{}, fmt.Errorf("%w: composition requires control, gateway, and runner roles", ErrInvalid)
	}
	control, err := NewControlRole(c)
	if err != nil {
		return Composition{}, err
	}
	gateway, err := NewGatewayRole(c)
	if err != nil {
		return Composition{}, err
	}
	runner, err := NewRunnerRole(c)
	if err != nil {
		return Composition{}, err
	}
	return Composition{control: control, gateway: gateway, runner: runner}, nil
}
func (c Composition) Control() RoleProjection { return c.control }
func (c Composition) Gateway() RoleProjection { return c.gateway }
func (c Composition) Runner() RoleProjection  { return c.runner }

// Lifecycle starts components in order, waits for each readiness signal, and
// rolls already-started components back in reverse order on any failure.
type Component interface {
	Start(context.Context) error
	Ready(context.Context) error
	Stop(context.Context) error
}
type Lifecycle struct {
	components []Component
	started    []Component
}

func NewLifecycle(components ...Component) *Lifecycle {
	return &Lifecycle{components: append([]Component(nil), components...)}
}
func (l *Lifecycle) Start(ctx context.Context) error {
	for _, component := range l.components {
		if err := component.Start(ctx); err != nil {
			_ = l.stopStarted(ctx)
			return err
		}
		l.started = append(l.started, component)
		if err := component.Ready(ctx); err != nil {
			_ = l.stopStarted(ctx)
			return err
		}
	}
	return nil
}
func (l *Lifecycle) Stop(ctx context.Context) error { return l.stopStarted(ctx) }
func (l *Lifecycle) stopStarted(ctx context.Context) error {
	var first error
	for i := len(l.started) - 1; i >= 0; i-- {
		if err := l.started[i].Stop(ctx); err != nil && first == nil {
			first = err
		}
	}
	l.started = nil
	return first
}
func StopWithin(l *Lifecycle, parent context.Context, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return l.Stop(ctx)
}
