package control

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// DeployClaimTTL is the default lifetime of an unclaimed deploy before it is
// garbage-collected. Operators can override it per-store with WithDeployClaimTTL.
const DeployClaimTTL = 5 * time.Minute

type Option func(*Store)

// WithClock lets tests provide deterministic timestamps.
func WithClock(now func() time.Time) Option {
	return func(s *Store) {
		if now != nil {
			s.now = now
		}
	}
}

// WithMaxSessionsPerAppPerDay caps how many sessions a single app may start in
// any rolling 24-hour window, a platform-wide abuse/DDoS control. 0 disables it.
func WithMaxSessionsPerAppPerDay(n int) Option {
	return func(s *Store) { s.maxSessionsPerAppPerDay = n }
}

// WithAnonymousPreview enables anonymous preview run: any deploy is runnable by
// id at "preview-<deployID>" in the tightest sandbox (no owner capabilities).
// It is gated because it lets unclaimed code run; enable it only with deploy
// rate limiting in place.
func WithAnonymousPreview(enabled bool) Option {
	return func(s *Store) { s.anonymousPreview = enabled }
}

// WithMaxDeployClaimsPerHour caps how many new deploy claims may be created
// across the platform in any rolling hour — the primary control against
// anonymous-deploy flooding (deploy is gated harder than run). 0 disables it.
func WithMaxDeployClaimsPerHour(n int) Option {
	return func(s *Store) { s.maxDeployClaimsPerHour = n }
}

// WithDeployClaimTTL sets how long an unclaimed deploy survives before it is
// garbage-collected. Non-positive values keep the default (DeployClaimTTL).
func WithDeployClaimTTL(d time.Duration) Option {
	return func(s *Store) {
		if d > 0 {
			s.deployClaimTTL = d
		}
	}
}

// WithDefaultMaxApps caps how many apps a single owner may create, applied to
// any owner without an explicit MaxApps quota. 0 leaves owners uncapped.
func WithDefaultMaxApps(n int) Option {
	return func(s *Store) { s.defaultMaxApps = n }
}

// WithBlobDir stores compiled WASM artifacts as files under dir instead of
// inside the JSON state file — durable artifact storage that keeps large
// binaries out of the metadata snapshot. The directory is created on first use.
func WithBlobDir(dir string) Option {
	return func(s *Store) {
		if dir != "" {
			s.blobs = &fsBlobStore{dir: dir}
		}
	}
}

// Store is a concurrency-safe control-plane repository. It is in-memory by
// default; OpenStore enables local JSON snapshot persistence. A SQL
// implementation can satisfy the same behavior later.
type Store struct {
	mu  sync.RWMutex
	now func() time.Time
	seq map[string]int

	persistPath string

	owners        map[string]Owner
	ownerByHandle map[string]string
	identities    map[identityKey]AuthIdentity

	apps           map[string]App
	appByOwnerName map[appKey]string

	artifacts map[string]Artifact
	blobs     BlobStore
	deploys   map[string]Deploy

	sshKeys             map[string]SSHKey
	sshKeyByFingerprint map[string]string

	ciTokens map[string]CIToken
	secrets  map[secretKey]SecretMetadata
	// secretValues holds the actual secret bytes, kept separate from the
	// value-free SecretMetadata. Injected into a claimed app's runtime as the
	// Env capability; never returned through list/metadata APIs.
	secretValues map[secretKey][]byte
	// egressAllow is the per-app outbound HTTP allowlist (host patterns). An app
	// with no entry has default-deny egress.
	egressAllow map[string][]string
	sessions    map[string]Session
	quotas      map[string]Quotas

	// suspendedDeploys is the operator kill switch at deploy granularity. Deploy
	// records stay immutable; suspension is tracked separately.
	suspendedDeploys map[string]struct{}

	// maxSessionsPerAppPerDay caps new sessions per app in any rolling 24h
	// window. 0 disables the cap.
	maxSessionsPerAppPerDay int

	// deploy-claim rate limiting: recentDeployClaims holds the timestamps of new
	// deploy claims in the trailing hour, gating the crown-jewel action against
	// anonymous-deploy flooding. 0 disables the cap.
	maxDeployClaimsPerHour int
	recentDeployClaims     []time.Time

	// deployClaimTTL is how long an unclaimed deploy survives before GC.
	// Defaults to DeployClaimTTL; set via WithDeployClaimTTL.
	deployClaimTTL time.Duration

	// defaultMaxApps caps apps per owner for owners without an explicit MaxApps
	// quota. 0 leaves them uncapped; set via WithDefaultMaxApps.
	defaultMaxApps int

	// anonymousPreview enables running any deploy by id at the handle
	// "preview-<deployID>" in the tightest (ownerless) sandbox — no secrets, no
	// egress. Off by default; gated on because it relies on deploy rate limiting.
	anonymousPreview bool

	// subMu guards subs, the set of change subscribers notified when runtime
	// state that dashboards display (sessions) mutates. It is independent of mu so
	// notifications can fire while a write lock is held.
	subMu sync.Mutex
	subs  map[*subscriber]struct{}
}

// subscriber is one change listener. ch is buffered to length 1 so notifications
// coalesce: a pending signal is enough to tell the reader "state changed, re-read".
type subscriber struct {
	ch chan struct{}
}

// Subscribe registers a listener for runtime-state changes. The returned channel
// receives a coalesced signal whenever sessions change; the caller re-reads the
// store to get the new state. The cancel func unregisters and must be called when
// the listener goes away.
func (s *Store) Subscribe() (<-chan struct{}, func()) {
	sub := &subscriber{ch: make(chan struct{}, 1)}
	s.subMu.Lock()
	if s.subs == nil {
		s.subs = make(map[*subscriber]struct{})
	}
	s.subs[sub] = struct{}{}
	s.subMu.Unlock()
	return sub.ch, func() {
		s.subMu.Lock()
		delete(s.subs, sub)
		s.subMu.Unlock()
	}
}

// notifyChange wakes every subscriber. The send is non-blocking: a subscriber
// that already has a pending signal keeps it (coalescing), and a slow reader
// never stalls a writer.
func (s *Store) notifyChange() {
	s.subMu.Lock()
	defer s.subMu.Unlock()
	for sub := range s.subs {
		select {
		case sub.ch <- struct{}{}:
		default:
		}
	}
}

type appKey struct {
	ownerID string
	name    string
}

type identityKey struct {
	provider AuthProvider
	subject  string
}

type secretKey struct {
	appID string
	key   string
}

func NewStore(opts ...Option) *Store {
	s := &Store{
		now:                 time.Now,
		deployClaimTTL:      DeployClaimTTL,
		seq:                 make(map[string]int),
		owners:              make(map[string]Owner),
		ownerByHandle:       make(map[string]string),
		identities:          make(map[identityKey]AuthIdentity),
		apps:                make(map[string]App),
		appByOwnerName:      make(map[appKey]string),
		artifacts:           make(map[string]Artifact),
		blobs:               newMemBlobStore(),
		deploys:             make(map[string]Deploy),
		sshKeys:             make(map[string]SSHKey),
		sshKeyByFingerprint: make(map[string]string),
		ciTokens:            make(map[string]CIToken),
		secrets:             make(map[secretKey]SecretMetadata),
		secretValues:        make(map[secretKey][]byte),
		egressAllow:         make(map[string][]string),
		sessions:            make(map[string]Session),
		quotas:              make(map[string]Quotas),
		suspendedDeploys:    make(map[string]struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// DeployClaimTTL returns the configured unclaimed-deploy lifetime, so callers
// (e.g. cleanup schedulers) stay consistent with the store's expiry policy.
func (s *Store) DeployClaimTTL() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.deployClaimTTL
}

func (s *Store) EnsureOwnerForIdentity(in IdentityInput) (Owner, AuthIdentity, error) {
	if !validProvider(in.Provider) {
		return Owner{}, AuthIdentity{}, fmt.Errorf("%w: unknown identity provider %q", ErrInvalid, in.Provider)
	}
	if err := validateNonEmpty("identity subject", in.Subject); err != nil {
		return Owner{}, AuthIdentity{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	key := identityKey{provider: in.Provider, subject: in.Subject}
	if identity, ok := s.identities[key]; ok {
		owner, ok := s.owners[identity.OwnerID]
		if !ok {
			return Owner{}, AuthIdentity{}, fmt.Errorf("%w: owner %q", ErrNotFound, identity.OwnerID)
		}
		return owner, identity, nil
	}

	owner := Owner{
		ID:        s.nextID("own"),
		CreatedAt: s.now(),
	}
	identity := AuthIdentity{
		Provider:  in.Provider,
		Subject:   in.Subject,
		OwnerID:   owner.ID,
		CreatedAt: s.now(),
	}
	s.owners[owner.ID] = owner
	s.identities[key] = identity
	if err := s.persistLocked(); err != nil {
		return Owner{}, AuthIdentity{}, err
	}
	return owner, identity, nil
}

func (s *Store) CreateOwner(handle string) (Owner, error) {
	if err := ValidateName(handle); err != nil {
		return Owner{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.ownerByHandle[handle]; ok {
		return Owner{}, fmt.Errorf("%w: owner %q already exists", ErrConflict, handle)
	}
	o := Owner{ID: s.nextID("own"), Handle: handle, HandleClaimed: true, CreatedAt: s.now()}
	s.owners[o.ID] = o
	s.ownerByHandle[o.Handle] = o.ID
	if err := s.persistLocked(); err != nil {
		return Owner{}, err
	}
	return o, nil
}

func (s *Store) EnsureOwner(handle string) (Owner, error) {
	if err := ValidateName(handle); err != nil {
		return Owner{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if id, ok := s.ownerByHandle[handle]; ok {
		return s.owners[id], nil
	}
	o := Owner{ID: s.nextID("own"), Handle: handle, HandleClaimed: true, CreatedAt: s.now()}
	s.owners[o.ID] = o
	s.ownerByHandle[o.Handle] = o.ID
	if err := s.persistLocked(); err != nil {
		return Owner{}, err
	}
	return o, nil
}

func (s *Store) ClaimOwnerHandle(ownerID, handle string) (Owner, error) {
	if err := ValidateName(handle); err != nil {
		return Owner{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	owner, ok := s.owners[ownerID]
	if !ok {
		return Owner{}, fmt.Errorf("%w: owner %q", ErrNotFound, ownerID)
	}
	if owner.Handle == handle {
		if !owner.HandleClaimed {
			owner.HandleClaimed = true
			s.owners[owner.ID] = owner
			if err := s.persistLocked(); err != nil {
				return Owner{}, err
			}
		}
		return owner, nil
	}
	if existingOwnerID, ok := s.ownerByHandle[handle]; ok && existingOwnerID != owner.ID {
		return Owner{}, fmt.Errorf("%w: owner %q already exists", ErrConflict, handle)
	}
	if owner.Handle != "" {
		delete(s.ownerByHandle, owner.Handle)
	}
	owner.Handle = handle
	owner.HandleClaimed = true
	s.owners[owner.ID] = owner
	s.ownerByHandle[owner.Handle] = owner.ID
	if err := s.persistLocked(); err != nil {
		return Owner{}, err
	}
	return owner, nil
}

func (s *Store) GetOwner(id string) (Owner, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	o, ok := s.owners[id]
	if !ok {
		return Owner{}, fmt.Errorf("%w: owner %q", ErrNotFound, id)
	}
	return o, nil
}

func (s *Store) FindOwner(handle string) (Owner, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.ownerByHandle[handle]
	if !ok {
		return Owner{}, fmt.Errorf("%w: owner %q", ErrNotFound, handle)
	}
	return s.owners[id], nil
}

func (s *Store) CreateApp(in AppInput) (App, error) {
	if err := ValidateName(in.Name); err != nil {
		return App{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.owners[in.OwnerID]; !ok {
		return App{}, fmt.Errorf("%w: owner %q", ErrNotFound, in.OwnerID)
	}
	key := appKey{ownerID: in.OwnerID, name: in.Name}
	if _, ok := s.appByOwnerName[key]; ok {
		return App{}, fmt.Errorf("%w: app %q already exists", ErrConflict, in.Name)
	}
	if err := s.checkAppQuotaLocked(in.OwnerID); err != nil {
		return App{}, err
	}
	app := App{
		ID:        s.nextID("app"),
		OwnerID:   in.OwnerID,
		Name:      in.Name,
		CreatedAt: s.now(),
	}
	s.apps[app.ID] = app
	s.appByOwnerName[key] = app.ID
	if err := s.persistLocked(); err != nil {
		return App{}, err
	}
	return app, nil
}

func (s *Store) EnsureApp(in AppInput) (App, error) {
	if err := ValidateName(in.Name); err != nil {
		return App{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.owners[in.OwnerID]; !ok {
		return App{}, fmt.Errorf("%w: owner %q", ErrNotFound, in.OwnerID)
	}
	key := appKey{ownerID: in.OwnerID, name: in.Name}
	if id, ok := s.appByOwnerName[key]; ok {
		return s.apps[id], nil
	}
	if err := s.checkAppQuotaLocked(in.OwnerID); err != nil {
		return App{}, err
	}
	app := App{
		ID:        s.nextID("app"),
		OwnerID:   in.OwnerID,
		Name:      in.Name,
		CreatedAt: s.now(),
	}
	s.apps[app.ID] = app
	s.appByOwnerName[key] = app.ID
	if err := s.persistLocked(); err != nil {
		return App{}, err
	}
	return app, nil
}

func (s *Store) ListApps(ownerID string) ([]App, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.owners[ownerID]; !ok {
		return nil, fmt.Errorf("%w: owner %q", ErrNotFound, ownerID)
	}
	var out []App
	for _, app := range s.apps {
		if app.OwnerID == ownerID {
			out = append(out, app)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// AppDailyConnections reports how many sessions the app started in the trailing
// 24h window and the configured per-app daily cap. A cap of 0 means unlimited.
func (s *Store) AppDailyConnections(appID string) (used, cap int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cutoff := s.now().Add(-24 * time.Hour)
	for _, session := range s.sessions {
		if session.AppID == appID && session.StartedAt.After(cutoff) {
			used++
		}
	}
	return used, s.maxSessionsPerAppPerDay
}
