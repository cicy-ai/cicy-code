// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

package main

// employeeRoleSlug looks up an agent's role-template slug from agent_config.
// nil-safe (returns "" when the store isn't initialised, e.g. in unit tests).
//
// Role config now lives entirely in the role itself: a library role's
// role/meta.yaml (tools/greeting) + role.md/system.md, or a user-authored
// custom agent's AGENT.md. The old central ~/cicy-ai/db/employees.yaml override
// layer was retired — there is no employees.yaml, and no fallback.
func employeeRoleSlug(shortID string) string {
	if store == nil {
		return ""
	}
	var rt string
	_ = store.QueryRow("SELECT COALESCE(role_template,'') FROM agent_config WHERE pane_id=?", shortID+":main.0").Scan(&rt)
	return sanitizeTemplateSlug(rt)
}
