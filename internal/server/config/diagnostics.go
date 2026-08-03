package config

// Diagnostic is a non-fatal deployment, pairing, or safety observation. The
// typed assembly reports these to operators without changing startup policy.
type Diagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Diagnostics returns warnings only. It never reads secret files, mutates
// configuration, or turns an optional exposure into a startup failure.
func Diagnostics(c Config) []Diagnostic {
	var warnings []Diagnostic
	if !c.Exposure.HTTP.Enabled {
		warnings = append(warnings, Diagnostic{Code: "http-disabled", Message: "HTTP exposure is disabled"})
	}
	if !c.Exposure.SSH.Enabled {
		warnings = append(warnings, Diagnostic{Code: "ssh-disabled", Message: "SSH exposure is disabled"})
	}
	if !c.Exposure.Gateway.Enabled {
		warnings = append(warnings, Diagnostic{Code: "gateway-disabled", Message: "gateway exposure is disabled"})
	}
	if !c.Roles.Control || !c.Roles.Gateway || !c.Roles.Runner {
		warnings = append(warnings, Diagnostic{Code: "incomplete-role-composition", Message: "one or more operational roles are disabled"})
	}
	if c.Resources.AutoCapacity && c.Resources.MemoryLimitBytes == 0 {
		warnings = append(warnings, Diagnostic{Code: "capacity-not-materialized", Message: "adaptive capacity will be resolved at startup"})
	}
	return warnings
}
