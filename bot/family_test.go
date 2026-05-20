package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// mkUsers cria N usuarios com nomes/telefones convenientes pros testes.
// Telefones sao deterministicos pra evitar colisao com outros testes.
func mkUsers(t *testing.T, db *DB, names ...string) []*User {
	t.Helper()
	users := make([]*User, 0, len(names))
	for i, n := range names {
		u := &User{
			PhoneNumber:       fmt.Sprintf("55119999900%02d", i),
			Name:              n,
			GoogleCalendarID:  "x",
			GoogleCredentials: "x",
		}
		if err := db.CreateUser(u); err != nil {
			t.Fatalf("create user %s: %v", n, err)
		}
		users = append(users, u)
	}
	return users
}

// =========================================================================
// UserType / SetUserType
// =========================================================================

func TestUserTypeIsValid(t *testing.T) {
	cases := []struct {
		ut   UserType
		want bool
	}{
		{UserTypeComum, true},
		{UserTypeIdoso, true},
		{UserTypeResponsavel, true},
		{"", false},
		{"admin", false},
		{"COMUM", false}, // case sensitive
	}
	for _, c := range cases {
		t.Run(string(c.ut), func(t *testing.T) {
			if got := c.ut.IsValid(); got != c.want {
				t.Fatalf("IsValid(%q) = %v, want %v", c.ut, got, c.want)
			}
		})
	}
}

func TestValidateUserType(t *testing.T) {
	if err := ValidateUserType(UserTypeComum); err != nil {
		t.Fatalf("comum should be valid: %v", err)
	}
	if err := ValidateUserType("xpto"); err == nil {
		t.Fatal("xpto should be invalid")
	}
}

func TestUserTypeDefaultIsComum(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Default")

	got, err := db.GetUserByID(users[0].ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Type != UserTypeComum {
		t.Fatalf("expected default type %q, got %q", UserTypeComum, got.Type)
	}

	// Por telefone tambem
	gotByPhone, err := db.GetUserByPhone(users[0].PhoneNumber)
	if err != nil {
		t.Fatalf("GetUserByPhone: %v", err)
	}
	if gotByPhone.Type != UserTypeComum {
		t.Fatalf("expected default type via phone %q, got %q", UserTypeComum, gotByPhone.Type)
	}
}

func TestSetUserType_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Idosa")

	if err := db.SetUserType(users[0].ID, UserTypeIdoso); err != nil {
		t.Fatalf("SetUserType: %v", err)
	}

	got, err := db.GetUserByID(users[0].ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Type != UserTypeIdoso {
		t.Fatalf("expected %q, got %q", UserTypeIdoso, got.Type)
	}
}

func TestSetUserType_InvalidType(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Original")

	if err := db.SetUserType(users[0].ID, UserType("qualquer")); err == nil {
		t.Fatal("expected validation error for invalid type")
	}

	got, err := db.GetUserByID(users[0].ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Type != UserTypeComum {
		t.Fatalf("type should not have changed; got %q", got.Type)
	}
}

func TestSetUserType_UserNotFound(t *testing.T) {
	db := setupTestDB(t)

	err := db.SetUserType(99999, UserTypeIdoso)
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

// =========================================================================
// MarkUserMessageReceived
// =========================================================================

func TestMarkUserMessageReceived_NilBeforeFirst(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Test")

	got, err := db.GetUserByID(users[0].ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.LastUserMessageAt != nil {
		t.Fatalf("expected nil LastUserMessageAt, got %v", *got.LastUserMessageAt)
	}
}

func TestMarkUserMessageReceived_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Test")

	ts := time.Now().UTC().Truncate(time.Second)
	if err := db.MarkUserMessageReceived(users[0].ID, ts); err != nil {
		t.Fatalf("MarkUserMessageReceived: %v", err)
	}

	after, err := db.GetUserByID(users[0].ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if after.LastUserMessageAt == nil {
		t.Fatal("expected LastUserMessageAt non-nil after mark")
	}
	diff := ts.Sub(*after.LastUserMessageAt)
	if diff > time.Second || diff < -time.Second {
		t.Fatalf("timestamp drift: stored=%v, expected=~%v", *after.LastUserMessageAt, ts)
	}
}

func TestMarkUserMessageReceived_UserNotFound(t *testing.T) {
	db := setupTestDB(t)

	err := db.MarkUserMessageReceived(99999, time.Now())
	if !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("expected ErrUserNotFound, got %v", err)
	}
}

// =========================================================================
// LinkFamily
// =========================================================================

func TestLinkFamily_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Maria", "Joao")

	link, err := db.LinkFamily(users[0].ID, users[1].ID, "filha")
	if err != nil {
		t.Fatalf("LinkFamily: %v", err)
	}
	if link.ID == 0 {
		t.Fatal("expected non-zero link ID")
	}
	if link.GuardianID != users[0].ID || link.DependentID != users[1].ID {
		t.Fatalf("ids mismatch: link=%+v", link)
	}
	if link.Relationship != "filha" {
		t.Fatalf("relationship: got %q, want %q", link.Relationship, "filha")
	}
	want := DefaultFamilyNotifyPrefs()
	if link.Notify != want {
		t.Fatalf("notify defaults: got %+v, want %+v", link.Notify, want)
	}
	if time.Since(link.CreatedAt) > time.Minute {
		t.Fatalf("CreatedAt too old: %v", link.CreatedAt)
	}
}

func TestLinkFamily_SelfLinkBlocked(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Alice")

	_, err := db.LinkFamily(users[0].ID, users[0].ID, "")
	if !errors.Is(err, ErrFamilyLinkSelfLink) {
		t.Fatalf("expected ErrFamilyLinkSelfLink, got %v", err)
	}

	// Nenhuma row foi inserida.
	deps, err := db.GetDependents(users[0].ID)
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected 0 dependents, got %d", len(deps))
	}
}

func TestLinkFamily_DuplicateBlocked(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Maria", "Joao")

	if _, err := db.LinkFamily(users[0].ID, users[1].ID, "filha"); err != nil {
		t.Fatalf("first link: %v", err)
	}
	_, err := db.LinkFamily(users[0].ID, users[1].ID, "filha")
	if !errors.Is(err, ErrFamilyLinkDuplicate) {
		t.Fatalf("expected ErrFamilyLinkDuplicate, got %v", err)
	}
}

func TestLinkFamily_BothDirectionsAllowed(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A", "B")

	if _, err := db.LinkFamily(users[0].ID, users[1].ID, ""); err != nil {
		t.Fatalf("link A->B: %v", err)
	}
	if _, err := db.LinkFamily(users[1].ID, users[0].ID, ""); err != nil {
		t.Fatalf("link B->A: %v", err)
	}
}

func TestLinkFamily_GuardianMissing(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Dep")

	_, err := db.LinkFamily(99999, users[0].ID, "")
	if !errors.Is(err, ErrFamilyLinkUserNotFound) {
		t.Fatalf("expected ErrFamilyLinkUserNotFound, got %v", err)
	}
}

func TestLinkFamily_DependentMissing(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Guard")

	_, err := db.LinkFamily(users[0].ID, 99999, "")
	if !errors.Is(err, ErrFamilyLinkUserNotFound) {
		t.Fatalf("expected ErrFamilyLinkUserNotFound, got %v", err)
	}
}

func TestLinkFamily_EmptyRelationshipAllowed(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "G", "D")

	link, err := db.LinkFamily(users[0].ID, users[1].ID, "")
	if err != nil {
		t.Fatalf("LinkFamily: %v", err)
	}
	if link.Relationship != "" {
		t.Fatalf("expected empty relationship, got %q", link.Relationship)
	}
}

// =========================================================================
// UnlinkFamily
// =========================================================================

func TestUnlinkFamily_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "G", "D")

	if _, err := db.LinkFamily(users[0].ID, users[1].ID, ""); err != nil {
		t.Fatalf("LinkFamily: %v", err)
	}
	if err := db.UnlinkFamily(users[0].ID, users[1].ID); err != nil {
		t.Fatalf("UnlinkFamily: %v", err)
	}
	ok, err := db.IsGuardianOf(users[0].ID, users[1].ID)
	if err != nil {
		t.Fatalf("IsGuardianOf: %v", err)
	}
	if ok {
		t.Fatal("expected no link after unlink")
	}
}

func TestUnlinkFamily_NotFound(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "G", "D")

	err := db.UnlinkFamily(users[0].ID, users[1].ID)
	if !errors.Is(err, ErrFamilyLinkNotFound) {
		t.Fatalf("expected ErrFamilyLinkNotFound, got %v", err)
	}
}

// =========================================================================
// GetDependents / GetGuardians
// =========================================================================

func TestGetDependents_Empty(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Solo")

	deps, err := db.GetDependents(users[0].ID)
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(deps) != 0 {
		t.Fatalf("expected 0 deps, got %d", len(deps))
	}
}

func TestGetDependents_OrderedByName(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Guardian", "Carla", "ana", "Bruno")
	g := users[0]
	for _, dep := range users[1:] {
		if _, err := db.LinkFamily(g.ID, dep.ID, ""); err != nil {
			t.Fatalf("link %s: %v", dep.Name, err)
		}
	}

	deps, err := db.GetDependents(g.ID)
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(deps) != 3 {
		t.Fatalf("expected 3 deps, got %d", len(deps))
	}
	want := []string{"ana", "Bruno", "Carla"}
	for i, link := range deps {
		if link.Other == nil {
			t.Fatalf("link[%d].Other is nil", i)
		}
		if link.Other.Name != want[i] {
			t.Fatalf("dep[%d]: expected %q, got %q", i, want[i], link.Other.Name)
		}
	}
}

func TestGetDependents_HydratesOther(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Guard", "Dep")
	if _, err := db.LinkFamily(users[0].ID, users[1].ID, "filho"); err != nil {
		t.Fatalf("LinkFamily: %v", err)
	}

	deps, err := db.GetDependents(users[0].ID)
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}
	if deps[0].Other == nil {
		t.Fatal("Other is nil")
	}
	if deps[0].Other.PhoneNumber != users[1].PhoneNumber {
		t.Fatalf("phone mismatch: got %q, want %q", deps[0].Other.PhoneNumber, users[1].PhoneNumber)
	}
	if deps[0].Other.Name != "Dep" {
		t.Fatalf("name mismatch: got %q", deps[0].Other.Name)
	}
}

func TestGetDependents_NotifyFlagsRoundTrip(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Guard", "Dep")
	link, err := db.LinkFamily(users[0].ID, users[1].ID, "")
	if err != nil {
		t.Fatalf("LinkFamily: %v", err)
	}

	// Atualiza flags
	newPrefs := FamilyNotifyPrefs{OnMedicationMiss: false, OnInactivity: true, OnSevereSignal: false}
	if err := db.UpdateNotifyPreferences(link.ID, newPrefs); err != nil {
		t.Fatalf("UpdateNotifyPreferences: %v", err)
	}

	deps, err := db.GetDependents(users[0].ID)
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("expected 1 dep, got %d", len(deps))
	}
	if deps[0].Notify != newPrefs {
		t.Fatalf("prefs round-trip: got %+v, want %+v", deps[0].Notify, newPrefs)
	}
}

func TestGetGuardians_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Dep", "G1", "G2")
	dep := users[0]
	for _, g := range users[1:] {
		if _, err := db.LinkFamily(g.ID, dep.ID, ""); err != nil {
			t.Fatalf("link %s: %v", g.Name, err)
		}
	}

	guards, err := db.GetGuardians(dep.ID)
	if err != nil {
		t.Fatalf("GetGuardians: %v", err)
	}
	if len(guards) != 2 {
		t.Fatalf("expected 2 guardians, got %d", len(guards))
	}
	for i, g := range guards {
		if g.Other == nil {
			t.Fatalf("guards[%d].Other is nil", i)
		}
	}
	// Ordenado: G1 antes de G2 (LOWER asc)
	if guards[0].Other.Name != "G1" || guards[1].Other.Name != "G2" {
		t.Fatalf("order: got %q,%q want G1,G2", guards[0].Other.Name, guards[1].Other.Name)
	}
}

// =========================================================================
// IsGuardianOf
// =========================================================================

func TestIsGuardianOf_TrueAfterLink(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A", "B")
	if _, err := db.LinkFamily(users[0].ID, users[1].ID, ""); err != nil {
		t.Fatalf("LinkFamily: %v", err)
	}
	ok, err := db.IsGuardianOf(users[0].ID, users[1].ID)
	if err != nil {
		t.Fatalf("IsGuardianOf: %v", err)
	}
	if !ok {
		t.Fatal("expected true after link")
	}
}

func TestIsGuardianOf_FalseBeforeLink(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A", "B")
	ok, err := db.IsGuardianOf(users[0].ID, users[1].ID)
	if err != nil {
		t.Fatalf("IsGuardianOf: %v", err)
	}
	if ok {
		t.Fatal("expected false before link")
	}
}

func TestIsGuardianOf_DirectionalAsymmetry(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "A", "B")
	if _, err := db.LinkFamily(users[0].ID, users[1].ID, ""); err != nil {
		t.Fatalf("LinkFamily: %v", err)
	}
	// A -> B existe
	ok, err := db.IsGuardianOf(users[0].ID, users[1].ID)
	if err != nil {
		t.Fatalf("IsGuardianOf A->B: %v", err)
	}
	if !ok {
		t.Fatal("A->B should be true")
	}
	// B -> A nao
	ok, err = db.IsGuardianOf(users[1].ID, users[0].ID)
	if err != nil {
		t.Fatalf("IsGuardianOf B->A: %v", err)
	}
	if ok {
		t.Fatal("B->A should be false")
	}
}

// =========================================================================
// UpdateNotifyPreferences
// =========================================================================

func TestUpdateNotifyPreferences_HappyPath(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "G", "D")
	link, err := db.LinkFamily(users[0].ID, users[1].ID, "")
	if err != nil {
		t.Fatalf("LinkFamily: %v", err)
	}

	prefs := FamilyNotifyPrefs{OnMedicationMiss: true, OnInactivity: false, OnSevereSignal: true}
	if err := db.UpdateNotifyPreferences(link.ID, prefs); err != nil {
		t.Fatalf("UpdateNotifyPreferences: %v", err)
	}

	deps, err := db.GetDependents(users[0].ID)
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(deps) != 1 || deps[0].Notify != prefs {
		t.Fatalf("not persisted: got %+v", deps)
	}
}

func TestUpdateNotifyPreferences_NotFound(t *testing.T) {
	db := setupTestDB(t)

	err := db.UpdateNotifyPreferences(99999, DefaultFamilyNotifyPrefs())
	if !errors.Is(err, ErrFamilyLinkNotFound) {
		t.Fatalf("expected ErrFamilyLinkNotFound, got %v", err)
	}
}

func TestUpdateNotifyPreferences_AllFalse(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "G", "D")
	link, err := db.LinkFamily(users[0].ID, users[1].ID, "")
	if err != nil {
		t.Fatalf("LinkFamily: %v", err)
	}

	prefs := FamilyNotifyPrefs{} // all false
	if err := db.UpdateNotifyPreferences(link.ID, prefs); err != nil {
		t.Fatalf("UpdateNotifyPreferences: %v", err)
	}
	deps, err := db.GetDependents(users[0].ID)
	if err != nil {
		t.Fatalf("GetDependents: %v", err)
	}
	if len(deps) != 1 || deps[0].Notify != prefs {
		t.Fatalf("not persisted: got %+v", deps)
	}
}

// =========================================================================
// Migrations
// =========================================================================

func TestMigration_NewColumnsAreNullableOrDefaulted(t *testing.T) {
	db := setupTestDB(t)
	// Cria usuario sem setar Type; deve cair em default 'comum'
	users := mkUsers(t, db, "Pre")

	got, err := db.GetUserByID(users[0].ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.Type != UserTypeComum {
		t.Fatalf("expected default 'comum', got %q", got.Type)
	}
	if got.LastUserMessageAt != nil {
		t.Fatalf("expected NULL last_user_message_at, got %v", *got.LastUserMessageAt)
	}
}

func TestMigration_Idempotent(t *testing.T) {
	db := setupTestDB(t)
	for i := 0; i < 3; i++ {
		if err := db.migrate(); err != nil {
			t.Fatalf("migrate run %d: %v", i, err)
		}
	}
	// Cria user, garante schema final funciona.
	if err := db.CreateUser(&User{
		PhoneNumber: "5511000000000", Name: "X", GoogleCalendarID: "x", GoogleCredentials: "x",
	}); err != nil {
		t.Fatalf("create user after re-migrate: %v", err)
	}
}

// =========================================================================
// Audit log helpers
// =========================================================================

func TestAuditLogFamilyLinkCreated(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Guard", "Dep")
	audit := NewAuditLog(db)

	if err := audit.LogFamilyLinkCreated(users[0].ID, users[0].ID, users[1].ID, "filha"); err != nil {
		t.Fatalf("LogFamilyLinkCreated: %v", err)
	}
	entries, err := audit.Query(users[0].ID, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Action != "family_link_created" {
		t.Fatalf("action: got %q", entries[0].Action)
	}
	d := entries[0].Details
	wantSubs := []string{
		fmt.Sprintf("guardian_id=%d", users[0].ID),
		fmt.Sprintf("dependent_id=%d", users[1].ID),
		"relationship=filha",
	}
	for _, s := range wantSubs {
		if !strings.Contains(d, s) {
			t.Fatalf("details %q missing %q", d, s)
		}
	}
}

func TestAuditLogFamilyLinkRemoved(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Guard", "Dep")
	audit := NewAuditLog(db)

	if err := audit.LogFamilyLinkRemoved(users[0].ID, users[0].ID, users[1].ID); err != nil {
		t.Fatalf("LogFamilyLinkRemoved: %v", err)
	}
	entries, err := audit.Query(users[0].ID, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 1 || entries[0].Action != "family_link_removed" {
		t.Fatalf("entries: %+v", entries)
	}
	if !strings.Contains(entries[0].Details, fmt.Sprintf("dependent_id=%d", users[1].ID)) {
		t.Fatalf("details missing dependent_id: %q", entries[0].Details)
	}
}

func TestAuditLogFamilyNotifyPrefsUpdated(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Guard")
	audit := NewAuditLog(db)

	before := DefaultFamilyNotifyPrefs()
	after := FamilyNotifyPrefs{OnMedicationMiss: true, OnInactivity: false, OnSevereSignal: true}
	if err := audit.LogFamilyNotifyPrefsUpdated(users[0].ID, 42, before, after); err != nil {
		t.Fatalf("LogFamilyNotifyPrefsUpdated: %v", err)
	}
	entries, err := audit.Query(users[0].ID, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	d := entries[0].Details
	if !strings.Contains(d, "link_id=42") {
		t.Fatalf("missing link_id in %q", d)
	}
	if !strings.Contains(d, "before=med:true,inat:true,sig:true") {
		t.Fatalf("missing before block in %q", d)
	}
	if !strings.Contains(d, "after=med:true,inat:false,sig:true") {
		t.Fatalf("missing after block in %q", d)
	}
}

func TestAuditLogUserTypeChanged_DetailsContainBeforeAfter(t *testing.T) {
	db := setupTestDB(t)
	users := mkUsers(t, db, "Actor", "Target")
	audit := NewAuditLog(db)

	if err := audit.LogUserTypeChanged(users[0].ID, users[1].ID, UserTypeComum, UserTypeIdoso); err != nil {
		t.Fatalf("LogUserTypeChanged: %v", err)
	}
	entries, err := audit.Query(users[0].ID, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(entries) != 1 || entries[0].Action != "user_type_changed" {
		t.Fatalf("entries: %+v", entries)
	}
	d := entries[0].Details
	wantSubs := []string{
		fmt.Sprintf("target_user_id=%d", users[1].ID),
		"before=comum",
		"after=idoso",
	}
	for _, s := range wantSubs {
		if !strings.Contains(d, s) {
			t.Fatalf("details %q missing %q", d, s)
		}
	}
}
