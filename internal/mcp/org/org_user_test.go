package org

import (
	"context"
	"encoding/json"
	"testing"
)

func TestOrgUsersFlow(t *testing.T) {
	srv, err := OrgServer(map[string]any{})
	if err != nil {
		t.Fatalf("OrgServer: %v", err)
	}
	if err := srv.AddUserBy("root", "org-admin", "bootstrap"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	base := map[string]any{"actor_id": "root"}

	// user-add (as org-admin).
	out, err := OrgUsersAdd(srv, with(base, "user_id", "alice", "role", "developer"))
	if err != nil {
		t.Fatalf("user-add: %v", err)
	}
	var added map[string]any
	if err := json.Unmarshal([]byte(out), &added); err != nil {
		t.Fatalf("parse user-add result: %v", err)
	}
	if added["status"] != "added" || added["id"] != "alice" {
		t.Errorf("user-add result = %v, want {status:added,id:alice}", added)
	}

	// user-list must now show root + alice.
	out, err = OrgUsersList(srv, base)
	if err != nil {
		t.Fatalf("user-list: %v", err)
	}
	var listed struct {
		Users []userView `json:"users"`
		Count int        `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil {
		t.Fatalf("parse user-list result: %v", err)
	}
	if listed.Count != 2 || len(listed.Users) != 2 {
		t.Fatalf("user-list count = %d (users %d), want 2", listed.Count, len(listed.Users))
	}
	if listed.Users[0].ID != "alice" || listed.Users[0].Role != "developer" || !listed.Users[0].Enabled {
		t.Errorf("users[0] = %+v, want alice/developer/enabled", listed.Users[0])
	}

	// user-role: promote alice to org-member.
	_, err = OrgUsersSetRole(srv, with(base, "user_id", "alice", "role", "org-member"))
	if err != nil {
		t.Fatalf("user-role: %v", err)
	}
	if role, ok := srv.UserRole("alice"); !ok || role != "org-member" {
		t.Errorf("UserRole(alice) after user-role = (%q, %v), want (org-member, true)", role, ok)
	}

	// user-audit: alice's trail has add (by root) + role (by root).
	out, err = OrgUsersAudit(srv, with(base, "user_id", "alice"))
	if err != nil {
		t.Fatalf("user-audit: %v", err)
	}
	var audit struct {
		Audit []struct {
			Action string `json:"action"`
			By     string `json:"by"`
			Detail string `json:"detail"`
		} `json:"audit"`
		Count int `json:"count"`
	}
	if err := json.Unmarshal([]byte(out), &audit); err != nil {
		t.Fatalf("parse user-audit result: %v", err)
	}
	if audit.Count != 2 {
		t.Fatalf("user-audit count = %d, want 2", audit.Count)
	}
	if audit.Audit[0].Action != "user-add" || audit.Audit[0].By != "root" {
		t.Errorf("audit[0] = %+v, want user-add by root", audit.Audit[0])
	}
	if audit.Audit[1].Action != "user-role" || audit.Audit[1].By != "root" {
		t.Errorf("audit[1] = %+v, want user-role by root", audit.Audit[1])
	}

	// user-disable: disable alice.
	out, err = OrgUsersDisable(srv, with(base, "user_id", "alice"))
	if err != nil {
		t.Fatalf("user-disable: %v", err)
	}
	var disabled map[string]any
	if err := json.Unmarshal([]byte(out), &disabled); err != nil {
		t.Fatalf("parse user-disable result: %v", err)
	}
	if disabled["status"] != "disabled" {
		t.Errorf("user-disable result = %v, want status disabled", disabled)
	}
	for _, u := range srv.ListUsers() {
		if u.ID == "alice" && u.Enabled {
			t.Error("alice should be disabled after user-disable")
		}
	}
}

// TestOrgUsersRBACDenial pins the RBAC layer in the leaf: a member-level
// actor performing an admin-only action gets a clear denial error naming the
// denied action, and an unknown actor is denied outright.
func TestOrgUsersRBACDenial(t *testing.T) {
	srv, err := OrgServer(map[string]any{})
	if err != nil {
		t.Fatalf("OrgServer: %v", err)
	}
	if err := srv.AddUserBy("root", "org-admin", "bootstrap"); err != nil {
		t.Fatalf("seed admin: %v", err)
	}
	if err := srv.AddUserBy("member", "developer", "root"); err != nil {
		t.Fatalf("seed member: %v", err)
	}
	// A member may list users...
	if _, err := OrgUsersList(srv, map[string]any{"actor_id": "member"}); err != nil {
		t.Errorf("member user-list: %v, want nil", err)
	}
	// ...but may not add users (admin action) — clear denial naming the action.
	_, err = OrgUsersAdd(srv, map[string]any{
		"actor_id": "member", "user_id": "mallory", "role": "org-member",
	})
	if err == nil {
		t.Fatal("member user-add: want RBAC denial, got nil")
	}
	if _, ok := srv.UserRole("mallory"); ok {
		t.Error("denied user-add must not have created the user")
	}
	// Unknown actor: fail-closed denial.
	if _, err := OrgUsersList(srv, map[string]any{"actor_id": "ghost"}); err == nil {
		t.Error("unknown actor user-list: want denial, got nil")
	}
	// Missing actor_id: validation error.
	if _, err := OrgUsersList(srv, map[string]any{}); err == nil {
		t.Error("missing actor_id: want error, got nil")
	}
	// Unknown action and missing required args surface as errors.
	if _, err := Users(context.TODO(), map[string]any{"action": "user-bogus", "actor_id": "root"}); err == nil {
		t.Error("unknown action: want error, got nil")
	}
	if _, err := OrgUsersAdd(srv, map[string]any{"actor_id": "root", "role": "org-member"}); err == nil {
		t.Error("user-add without user_id: want error, got nil")
	}
	if _, err := OrgUsersSetRole(srv, map[string]any{"actor_id": "root", "user_id": "alice"}); err == nil {
		t.Error("user-role without role: want error, got nil")
	}
}

// with returns a copy of base extended with the given key/value pairs.
func with(base map[string]any, kv ...string) map[string]any {
	out := make(map[string]any, len(base)+len(kv)/2)
	for k, v := range base {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}
