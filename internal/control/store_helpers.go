package control

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (s *Store) nextID(prefix string) string {
	s.seq[prefix]++
	return fmt.Sprintf("%s_%06d", prefix, s.seq[prefix])
}

func (s *Store) migrateGeneratedIdentityHandlesLocked() bool {
	identityByOwner := make(map[string]identityKey, len(s.identities))
	for key, identity := range s.identities {
		if _, ok := identityByOwner[identity.OwnerID]; !ok {
			identityByOwner[identity.OwnerID] = key
		}
	}

	var ownerIDs []string
	for ownerID := range s.owners {
		ownerIDs = append(ownerIDs, ownerID)
	}
	sort.Strings(ownerIDs)

	migrated := false
	for _, ownerID := range ownerIDs {
		owner := s.owners[ownerID]
		key, ok := identityByOwner[owner.ID]
		if !ok || owner.HandleClaimed || !isGeneratedIdentityHandle(key, owner.Handle) {
			continue
		}
		delete(s.ownerByHandle, owner.Handle)
		owner.Handle = ""
		s.owners[owner.ID] = owner
		migrated = true
	}
	return migrated
}

func isGeneratedIdentityHandle(key identityKey, handle string) bool {
	if isLegacyIdentityHandle(handle) {
		return true
	}
	sum := sha256.Sum256([]byte(string(key.provider) + ":" + key.subject))
	for attempt := range len(sum) / 4 {
		if handle == legacyFriendlyIdentityHandle(sum, attempt) {
			return true
		}
	}
	return false
}

func isLegacyIdentityHandle(handle string) bool {
	rest, ok := strings.CutPrefix(handle, "user-")
	if !ok {
		return false
	}
	if left, right, ok := strings.Cut(rest, "-"); ok {
		return len(left) == 16 && allLowerHex(left) && right != "" && allDigits(right)
	}
	switch len(rest) {
	case 16, 24, 32, 48:
		return allLowerHex(rest)
	default:
		return false
	}
}

func allLowerHex(s string) bool {
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return false
		}
	}
	return true
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func legacyFriendlyIdentityHandle(sum [sha256.Size]byte, attempt int) string {
	offset := (attempt * 4) % len(sum)
	return fmt.Sprintf("%s-%s-%04d",
		legacyIdentityAdjectives[int(sum[offset])%len(legacyIdentityAdjectives)],
		legacyIdentityNouns[int(sum[offset+1])%len(legacyIdentityNouns)],
		(int(sum[offset+2])<<8|int(sum[offset+3]))%10_000,
	)
}

var legacyIdentityAdjectives = []string{
	"amber",
	"brave",
	"bright",
	"calm",
	"clear",
	"copper",
	"crisp",
	"direct",
	"early",
	"fresh",
	"golden",
	"green",
	"honest",
	"kind",
	"lively",
	"lucky",
	"modern",
	"noble",
	"plain",
	"quick",
	"quiet",
	"ready",
	"red",
	"sharp",
	"silver",
	"solid",
	"steady",
	"sunny",
	"swift",
	"true",
	"vivid",
	"warm",
}

var legacyIdentityNouns = []string{
	"anchor",
	"atlas",
	"beacon",
	"branch",
	"bridge",
	"brook",
	"cedar",
	"cliff",
	"compass",
	"copper",
	"field",
	"forge",
	"garden",
	"harbor",
	"lantern",
	"maple",
	"market",
	"meadow",
	"mesa",
	"north",
	"orchard",
	"peak",
	"ridge",
	"river",
	"signal",
	"summit",
	"terrace",
	"timber",
	"trail",
	"valley",
	"vista",
	"well",
}

func (s *Store) checkAppQuotaLocked(ownerID string) error {
	// An explicit per-owner quota wins; otherwise fall back to the platform
	// default (0 = uncapped).
	limit := s.quotas[ownerID].MaxApps
	if limit == 0 {
		limit = s.defaultMaxApps
	}
	if quotaExceeded(limit, s.apps, func(_ string, app App) bool { return app.OwnerID == ownerID }) {
		return fmt.Errorf("%w: owner app quota exceeded", ErrInvalid)
	}
	return nil
}

func (s *Store) checkDeployQuotaLocked(appID string) error {
	app := s.apps[appID]
	q := s.quotas[app.OwnerID]
	if quotaExceeded(q.MaxDeploysPerApp, s.deploys, func(_ string, deploy Deploy) bool { return deploy.AppID == appID }) {
		return fmt.Errorf("%w: app deploy quota exceeded", ErrInvalid)
	}
	return nil
}

func (s *Store) checkSecretQuotaLocked(appID string) error {
	app := s.apps[appID]
	q := s.quotas[app.OwnerID]
	if quotaExceeded(q.MaxSecretsPerApp, s.secrets, func(key secretKey, _ SecretMetadata) bool { return key.appID == appID }) {
		return fmt.Errorf("%w: app secret quota exceeded", ErrInvalid)
	}
	return nil
}

func (s *Store) checkSessionQuotaLocked(ownerID string) error {
	q := s.quotas[ownerID]
	if quotaExceeded(q.MaxSessions, s.sessions, func(_ string, session Session) bool {
		return session.EndedAt == nil && s.apps[session.AppID].OwnerID == ownerID
	}) {
		return fmt.Errorf("%w: owner session quota exceeded", ErrInvalid)
	}
	return nil
}

// checkAppDailySessionQuotaLocked enforces the per-app rolling 24h session cap.
func (s *Store) checkAppDailySessionQuotaLocked(appID string) error {
	cutoff := s.now().Add(-24 * time.Hour)
	if quotaExceeded(s.maxSessionsPerAppPerDay, s.sessions, func(_ string, session Session) bool {
		return session.AppID == appID && session.StartedAt.After(cutoff)
	}) {
		return fmt.Errorf("%w: app %q reached its daily session limit (%d/day)", ErrQuota, appID, s.maxSessionsPerAppPerDay)
	}
	return nil
}

func quotaExceeded[K comparable, V any](limit int, items map[K]V, match func(K, V) bool) bool {
	if limit <= 0 {
		return false
	}
	count := 0
	for key, value := range items {
		if match(key, value) {
			count++
		}
	}
	return count >= limit
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneBytes(in []byte) []byte {
	if len(in) == 0 {
		return nil
	}
	out := make([]byte, len(in))
	copy(out, in)
	return out
}

func digestBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func splitHandle(handle string) (owner, app string, ok bool) {
	owner, app, ok = strings.Cut(handle, "/")
	if !ok || owner == "" || app == "" || strings.Contains(app, "/") {
		return "", "", false
	}
	return owner, app, true
}

func cloneArtifact(in Artifact) Artifact {
	in.BuildMetadata = cloneStringMap(in.BuildMetadata)
	return in
}

func cloneDeploy(in Deploy) Deploy {
	if in.ClaimExpiresAt != nil {
		t := *in.ClaimExpiresAt
		in.ClaimExpiresAt = &t
	}
	if in.ClaimedAt != nil {
		t := *in.ClaimedAt
		in.ClaimedAt = &t
	}
	return in
}

func cloneScopes(in []TokenScope) []TokenScope {
	if len(in) == 0 {
		return nil
	}
	out := make([]TokenScope, len(in))
	copy(out, in)
	return out
}

func cloneToken(in CIToken) CIToken {
	in.Scopes = cloneScopes(in.Scopes)
	if in.RevokedAt != nil {
		t := *in.RevokedAt
		in.RevokedAt = &t
	}
	return in
}

func cloneSession(in Session) Session {
	if in.EndedAt != nil {
		t := *in.EndedAt
		in.EndedAt = &t
	}
	return in
}
