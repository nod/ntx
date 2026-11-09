package models

import (
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

type peopleFixture struct {
	db    *gorm.DB
	sites *SiteStore
	users *UserStore
	mems  *MembershipStore
	ids   *IdentityStore
	sess  *SessionStore
	a, b  *Site
	root  *User
}

func newPeople(t *testing.T) *peopleFixture {
	t.Helper()
	d := newTestSiteDB(t)
	f := &peopleFixture{
		db: d, sites: NewSiteStore(d), users: NewUserStore(d),
		mems: NewMembershipStore(d), ids: NewIdentityStore(d), sess: NewSessionStore(d),
	}
	f.a = mustCreateSite(t, f.sites, "a", "a.example.com")
	f.b = mustCreateSite(t, f.sites, "b", "b.example.com")
	root, err := f.users.EnsureRoot()
	if err != nil {
		t.Fatal(err)
	}
	f.root = root
	return f
}

func (f *peopleFixture) person(t *testing.T, email string) *User {
	t.Helper()
	u, err := f.users.FindOrCreateByEmail(email)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestRootGuards(t *testing.T) {
	f := newPeople(t)
	id := f.root.ID
	for name, err := range map[string]error{
		"SetEmail":                f.users.SetEmail(id, "root@x.io"),
		"SetPlatformAdmin(true)":  f.users.SetPlatformAdmin(id, true),
		"SetPlatformAdmin(false)": f.users.SetPlatformAdmin(id, false),
		"SetName":                 f.users.SetName(id, "Mallory"),
		"Delete":                  f.users.Delete(id),
		"Memberships.Add":         f.mems.Add(id, f.a.ID, RoleAdmin, false),
		"Identities.Link":         f.ids.Link(id, "iss", "sub", "root@x.io"),
	} {
		if !errors.Is(err, ErrRootProtected) {
			t.Errorf("%s on root = %v, want ErrRootProtected", name, err)
		}
	}
	got, err := f.users.GetByID(id)
	if err != nil || got == nil || !got.IsRoot || got.Email != "" || got.Name != "root" {
		t.Fatalf("root after refused writes = %+v, %v", got, err)
	}
	if ids, _ := f.ids.ListForUser(id); len(ids) != 0 {
		t.Fatalf("root has identities: %+v", ids)
	}
	if ms, _ := f.mems.ListForUser(id); len(ms) != 0 {
		t.Fatalf("root has memberships: %+v", ms)
	}
}

func TestFindOrCreateByEmailNormalises(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, " Bob@X.io ")
	if u.Email != "bob@x.io" {
		t.Fatalf("email = %q, want bob@x.io", u.Email)
	}
	again := f.person(t, "bob@x.io")
	if again.ID != u.ID {
		t.Fatalf("second call made a new user (%d vs %d)", again.ID, u.ID)
	}
	found, err := f.users.FindByEmail("BOB@x.io")
	if err != nil || found == nil || found.ID != u.ID {
		t.Fatalf("FindByEmail = %+v, %v", found, err)
	}
	if _, err := f.users.FindOrCreateByEmail("nope"); !isValidation(err) {
		t.Fatalf("FindOrCreateByEmail(nope) = %v, want a validation error", err)
	}
	if none, err := f.users.FindByEmail(""); err != nil || none != nil {
		t.Fatalf("FindByEmail(\"\") = %+v, %v; root must never match", none, err)
	}
}

func TestMembershipAddTwiceRejected(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "c@x.io")
	if err := f.mems.Add(u.ID, f.a.ID, RoleContributor, true); err != nil {
		t.Fatal(err)
	}
	if err := f.mems.Add(u.ID, f.a.ID, RoleAdmin, false); !isValidation(err) {
		t.Fatalf("second Add = %v, want a validation error", err)
	}
	if err := f.mems.Add(u.ID, f.b.ID, "owner", false); !isValidation(err) {
		t.Fatalf("Add with role owner = %v, want a validation error", err)
	}
	m, err := f.mems.Get(u.ID, f.a.ID)
	if err != nil || m == nil || m.Role != RoleContributor || !m.CanPublish {
		t.Fatalf("Get = %+v, %v", m, err)
	}
	if m, _ := f.mems.Get(u.ID, f.b.ID); m != nil {
		t.Fatalf("Get on site B = %+v, want nil", m)
	}
	counts, err := f.mems.CountBySite()
	if err != nil || counts[f.a.ID] != 1 || counts[f.b.ID] != 0 {
		t.Fatalf("CountBySite = %v, %v", counts, err)
	}
}

func TestReassignedAccountGuard(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "u@x.io")
	other := f.person(t, "o@x.io")
	if err := f.ids.Link(u.ID, "iss", "sub1", "u@x.io"); err != nil {
		t.Fatal(err)
	}
	if err := f.ids.Link(u.ID, "iss", "sub1", "u@x.io"); err != nil {
		t.Fatalf("re-linking the same identity = %v, want nil", err)
	}
	if err := f.ids.Link(u.ID, "iss", "sub2", "u@x.io"); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("second subject for the same issuer = %v, want ErrIdentityConflict", err)
	}
	if err := f.ids.Link(u.ID, "other-iss", "sub2", "u@x.io"); err != nil {
		t.Fatalf("another issuer = %v, want nil", err)
	}
	if err := f.ids.Link(other.ID, "iss", "sub1", "o@x.io"); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("someone else's identity = %v, want ErrIdentityConflict", err)
	}
	got, err := f.ids.Find("iss", "sub1")
	if err != nil || got == nil || got.UserID != u.ID {
		t.Fatalf("Find = %+v, %v", got, err)
	}
	ids, _ := f.ids.ListForUser(u.ID)
	if len(ids) != 2 {
		t.Fatalf("identities = %+v, want 2", ids)
	}
	if err := f.ids.Unlink(ids[0].ID, other.ID); err == nil {
		t.Fatal("Unlink with the wrong user ID succeeded")
	}
	if err := f.ids.Unlink(ids[0].ID, u.ID); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteUserKeepsPosts(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "gone@x.io")
	if err := f.mems.Add(u.ID, f.a.ID, RoleContributor, false); err != nil {
		t.Fatal(err)
	}
	if err := f.ids.Link(u.ID, "iss", "s", u.Email); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sess.Create(u.ID, &f.a.ID, "", time.Hour); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := f.db.Exec(`INSERT INTO posts (site_id, author_id, slug, title, body, published, created_at, updated_at)
		VALUES (?, ?, 'kept', 'Kept', 'body', TRUE, ?, ?)`, f.a.ID, u.ID, now, now).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.users.Delete(u.ID); err != nil {
		t.Fatal(err)
	}
	var author *int64
	var title string
	row := f.db.Raw("SELECT author_id, title FROM posts WHERE slug = 'kept'").Row()
	if err := row.Scan(&author, &title); err != nil {
		t.Fatal(err)
	}
	if author != nil || title != "Kept" {
		t.Fatalf("post after author deletion: author=%v title=%q", author, title)
	}
	for _, table := range []string{"memberships", "identities", "sessions"} {
		if n := countRows(t, f.db, table); n != 0 {
			t.Errorf("%s rows after delete = %d, want 0", table, n)
		}
	}
}

func TestDeleteForUserRevokesOnlyThatUser(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "u@x.io")
	o := f.person(t, "o@x.io")
	tu, _ := f.sess.Create(u.ID, &f.a.ID, "", time.Hour)
	to, _ := f.sess.Create(o.ID, &f.a.ID, "", time.Hour)
	if err := f.sess.DeleteForUser(u.ID); err != nil {
		t.Fatal(err)
	}
	if s, _ := f.sess.Lookup(tu, time.Now()); s != nil {
		t.Fatal("u's session survived DeleteForUser")
	}
	if s, _ := f.sess.Lookup(to, time.Now()); s == nil {
		t.Fatal("o's session was deleted too")
	}
}

func TestUserEdits(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "u@x.io")
	f.person(t, "taken@x.io")
	if err := f.users.SetEmail(u.ID, "Taken@x.io"); !isValidation(err) {
		t.Fatalf("SetEmail to a used address = %v, want a validation error", err)
	}
	if err := f.users.SetEmail(u.ID, " New@X.io"); err != nil {
		t.Fatal(err)
	}
	if err := f.users.SetPlatformAdmin(u.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := f.users.SetName(u.ID, "Una"); err != nil {
		t.Fatal(err)
	}
	got, _ := f.users.GetByID(u.ID)
	if got.Email != "new@x.io" || !got.IsPlatformAdmin || got.Name != "Una" {
		t.Fatalf("user = %+v", got)
	}
	all, err := f.users.List()
	if err != nil || len(all) != 3 || !all[0].IsRoot {
		t.Fatalf("List = %+v, %v; want root first", all, err)
	}
	byID, err := f.users.GetByIDs([]int64{u.ID, 9999})
	if err != nil || byID[u.ID] == nil || byID[9999] != nil {
		t.Fatalf("GetByIDs = %v, %v", byID, err)
	}
}
