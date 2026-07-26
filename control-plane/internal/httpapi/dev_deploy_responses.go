package httpapi

import "github.com/Ceinl/plumtree/control-plane/internal/control"

func devDeployResponse(owner control.Owner, app control.App, deploy control.Deploy, claimed bool, claimURL, claimToken string) map[string]any {
	appHandle := publicAppHandle(owner, app.Name)
	deployJSON := map[string]any{
		"id":      deploy.ID,
		"claimed": claimed,
	}
	if claimURL != "" {
		deployJSON["claimUrl"] = claimURL
	}
	if claimToken != "" {
		deployJSON["claimToken"] = claimToken
	}
	if deploy.ClaimExpiresAt != nil && !claimed {
		deployJSON["claimExpiresAt"] = deploy.ClaimExpiresAt
	}
	out := map[string]any{
		"app": map[string]any{
			"id":             app.ID,
			"name":           app.Name,
			"handle":         appHandle,
			"activeDeployId": app.ActiveDeployID,
		},
		"deploy": deployJSON,
	}
	if claimURL != "" {
		out["claimUrl"] = claimURL
	}
	return out
}

func inspectResponse(owner control.Owner, app control.App, deploy control.Deploy, artifact control.Artifact) map[string]any {
	appHandle := publicAppHandle(owner, app.Name)
	return map[string]any{
		"app": map[string]any{
			"id":             app.ID,
			"name":           firstNonEmpty(app.Name, deploy.AppName),
			"handle":         appHandle,
			"activeDeployId": app.ActiveDeployID,
			"claimed":        deploy.CreatedByOwnerID != "",
		},
		"deploy": map[string]any{
			"id":           deploy.ID,
			"appType":      deploy.AppType,
			"sourceDigest": deploy.SourceDigest,
			"createdAt":    deploy.CreatedAt,
			"claimedAt":    deploy.ClaimedAt,
		},
		"artifact": map[string]any{
			"id":            artifact.ID,
			"digest":        artifact.Digest,
			"sizeBytes":     artifact.SizeBytes,
			"abiVersion":    artifact.ABIVersion,
			"buildMetadata": artifact.BuildMetadata,
			"createdAt":     artifact.CreatedAt,
		},
	}
}

func publicAppHandle(owner control.Owner, appName string) string {
	if owner.Handle == "" || appName == "" {
		return ""
	}
	if owner.Internal && owner.Handle == control.AutoClaimOwnerHandle {
		return appName
	}
	return owner.Handle + "/" + appName
}
