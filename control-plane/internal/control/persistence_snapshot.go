package control

import "sort"

func (s *Store) snapshotLocked() storeSnapshot {
	snap := storeSnapshot{
		Version: storeSnapshotVersion,
		Seq:     make(map[string]int, len(s.seq)),
	}
	for k, v := range s.seq {
		snap.Seq[k] = v
	}

	for _, owner := range s.owners {
		snap.Owners = append(snap.Owners, owner)
	}
	sort.Slice(snap.Owners, func(i, j int) bool { return snap.Owners[i].ID < snap.Owners[j].ID })

	for _, identity := range s.identities {
		snap.Identities = append(snap.Identities, identity)
	}
	sort.Slice(snap.Identities, func(i, j int) bool {
		if snap.Identities[i].Provider != snap.Identities[j].Provider {
			return snap.Identities[i].Provider < snap.Identities[j].Provider
		}
		return snap.Identities[i].Subject < snap.Identities[j].Subject
	})

	for _, app := range s.apps {
		snap.Apps = append(snap.Apps, app)
	}
	sort.Slice(snap.Apps, func(i, j int) bool { return snap.Apps[i].ID < snap.Apps[j].ID })

	for _, artifact := range s.artifacts {
		snap.Artifacts = append(snap.Artifacts, cloneArtifact(artifact))
	}
	sort.Slice(snap.Artifacts, func(i, j int) bool { return snap.Artifacts[i].ID < snap.Artifacts[j].ID })

	// A durable blob store keeps bytes on disk and returns nil here, so large
	// artifacts stay out of the metadata snapshot.
	if blobs := s.blobs.snapshot(); len(blobs) > 0 {
		snap.Blobs = blobs
	}

	for _, deploy := range s.deploys {
		snap.Deploys = append(snap.Deploys, cloneDeploy(deploy))
	}
	sort.Slice(snap.Deploys, func(i, j int) bool { return snap.Deploys[i].ID < snap.Deploys[j].ID })

	for _, key := range s.sshKeys {
		snap.SSHKeys = append(snap.SSHKeys, key)
	}
	sort.Slice(snap.SSHKeys, func(i, j int) bool { return snap.SSHKeys[i].ID < snap.SSHKeys[j].ID })

	for _, token := range s.ciTokens {
		snap.CITokens = append(snap.CITokens, cloneToken(token))
	}
	sort.Slice(snap.CITokens, func(i, j int) bool { return snap.CITokens[i].ID < snap.CITokens[j].ID })

	for _, secret := range s.secrets {
		snap.Secrets = append(snap.Secrets, secret)
	}
	sort.Slice(snap.Secrets, func(i, j int) bool {
		if snap.Secrets[i].AppID != snap.Secrets[j].AppID {
			return snap.Secrets[i].AppID < snap.Secrets[j].AppID
		}
		return snap.Secrets[i].Key < snap.Secrets[j].Key
	})

	for k, v := range s.secretValues {
		snap.SecretValues = append(snap.SecretValues, persistedSecretValue{
			AppID: k.appID, Key: k.key, Value: append([]byte(nil), v...),
		})
	}
	sort.Slice(snap.SecretValues, func(i, j int) bool {
		if snap.SecretValues[i].AppID != snap.SecretValues[j].AppID {
			return snap.SecretValues[i].AppID < snap.SecretValues[j].AppID
		}
		return snap.SecretValues[i].Key < snap.SecretValues[j].Key
	})

	if len(s.egressAllow) > 0 {
		snap.EgressAllow = make(map[string][]string, len(s.egressAllow))
		for appID, hosts := range s.egressAllow {
			snap.EgressAllow[appID] = append([]string(nil), hosts...)
		}
	}

	for _, session := range s.sessions {
		snap.Sessions = append(snap.Sessions, cloneSession(session))
	}
	sort.Slice(snap.Sessions, func(i, j int) bool { return snap.Sessions[i].ID < snap.Sessions[j].ID })

	if len(s.quotas) > 0 {
		snap.Quotas = make(map[string]Quotas, len(s.quotas))
		for ownerID, quotas := range s.quotas {
			snap.Quotas[ownerID] = quotas
		}
	}

	for deployID := range s.suspendedDeploys {
		snap.SuspendedDeploys = append(snap.SuspendedDeploys, deployID)
	}
	sort.Strings(snap.SuspendedDeploys)
	return snap
}
