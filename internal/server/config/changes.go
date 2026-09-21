package config

import "reflect"

// ChangeSet describes configuration edits that take effect on the next
// process start. The additive assembly does not hot-mutate live components.
type ChangeSet struct {
	RestartRequired bool     `json:"restartRequired"`
	Reasons         []string `json:"reasons,omitempty"`
}

// ChangesRequireRestart classifies a change without applying it. Every
// materialized operational setting is restart-only in this first assembly.
func ChangesRequireRestart(before, after Config) ChangeSet {
	if reflect.DeepEqual(before, after) {
		return ChangeSet{}
	}
	return ChangeSet{RestartRequired: true, Reasons: []string{"configuration changes apply on restart"}}
}
