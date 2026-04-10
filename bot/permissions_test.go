package main

import (
	"testing"
)

func TestGrantAndCheckPermission(t *testing.T) {
	db := setupTestDB(t)
	pm := NewPermissionManager(db)

	alice := &User{ID: 1, Name: "Alice", PhoneNumber: "111"}
	bob := &User{ID: 2, Name: "Bob", PhoneNumber: "222"}

	// Before grant: no permission
	ok, err := pm.CanScheduleFor(alice.ID, bob.ID)
	if err != nil {
		t.Fatalf("CanScheduleFor error: %v", err)
	}
	if ok {
		t.Fatal("expected no permission before grant")
	}

	// Grant: alice can schedule for bob
	if err := pm.Grant(alice.ID, bob.ID); err != nil {
		t.Fatalf("Grant error: %v", err)
	}

	ok, err = pm.CanScheduleFor(alice.ID, bob.ID)
	if err != nil {
		t.Fatalf("CanScheduleFor after grant error: %v", err)
	}
	if !ok {
		t.Fatal("expected permission after grant")
	}

	// Permission is unidirectional: bob cannot schedule for alice
	ok, err = pm.CanScheduleFor(bob.ID, alice.ID)
	if err != nil {
		t.Fatalf("CanScheduleFor reverse error: %v", err)
	}
	if ok {
		t.Fatal("expected no reverse permission")
	}

	// Revoke
	if err := pm.Revoke(alice.ID, bob.ID); err != nil {
		t.Fatalf("Revoke error: %v", err)
	}

	ok, err = pm.CanScheduleFor(alice.ID, bob.ID)
	if err != nil {
		t.Fatalf("CanScheduleFor after revoke error: %v", err)
	}
	if ok {
		t.Fatal("expected no permission after revoke")
	}
}

func TestListGranteesForUser(t *testing.T) {
	db := setupTestDB(t)
	pm := NewPermissionManager(db)

	// Create users in db so we can look them up by name
	alice := &User{ID: 0, Name: "Alice", PhoneNumber: "111"}
	bob := &User{ID: 0, Name: "Bob", PhoneNumber: "222"}
	carol := &User{ID: 0, Name: "Carol", PhoneNumber: "333"}

	if err := db.CreateUser(alice); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := db.CreateUser(bob); err != nil {
		t.Fatalf("create bob: %v", err)
	}
	if err := db.CreateUser(carol); err != nil {
		t.Fatalf("create carol: %v", err)
	}

	// Bob and Carol grant alice access to their calendars
	if err := pm.Grant(alice.ID, bob.ID); err != nil {
		t.Fatalf("Grant alice->bob: %v", err)
	}
	if err := pm.Grant(alice.ID, carol.ID); err != nil {
		t.Fatalf("Grant alice->carol: %v", err)
	}

	targets, err := pm.ListTargetsFor(alice.ID)
	if err != nil {
		t.Fatalf("ListTargetsFor error: %v", err)
	}
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
}

func TestResolveTargetUser(t *testing.T) {
	db := setupTestDB(t)
	pm := NewPermissionManager(db)

	alice := &User{Name: "Alice", PhoneNumber: "111"}
	bob := &User{Name: "Bob Marley", PhoneNumber: "222"}

	if err := db.CreateUser(alice); err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := db.CreateUser(bob); err != nil {
		t.Fatalf("create bob: %v", err)
	}

	// Exact match
	found, err := pm.ResolveByName("Bob Marley")
	if err != nil {
		t.Fatalf("ResolveByName exact: %v", err)
	}
	if found == nil || found.Name != "Bob Marley" {
		t.Fatalf("expected Bob Marley, got %v", found)
	}

	// Case-insensitive partial match
	found, err = pm.ResolveByName("bob")
	if err != nil {
		t.Fatalf("ResolveByName case-insensitive: %v", err)
	}
	if found == nil || found.Name != "Bob Marley" {
		t.Fatalf("expected Bob Marley for 'bob', got %v", found)
	}

	// Not found
	found, err = pm.ResolveByName("Unknown")
	if err != nil {
		t.Fatalf("ResolveByName not found error: %v", err)
	}
	if found != nil {
		t.Fatalf("expected nil for unknown name, got %v", found)
	}
}
