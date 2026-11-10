package models

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

// Unique fields: when another request writes the same value after a save's
// pre-check, the save must still end in the pre-check's own outcome -- a
// form error, or finding the row the other request made -- never a raw
// constraint failure (a 500). See race_test.go for how these hold the other
// request's write open.

func wantInvalid(t *testing.T, err error, substr string) {
	t.Helper()
	var v *ValidationError
	if !errors.As(err, &v) || !strings.Contains(v.Msg, substr) {
		t.Fatalf("got %v, want a ValidationError mentioning %q", err, substr)
	}
}

func mustExec(t *testing.T, res *gorm.DB) {
	t.Helper()
	if res.Error != nil {
		t.Fatal(res.Error)
	}
}

func TestSetEmailRace(t *testing.T) {
	f := newPeople(t)
	one, two := f.person(t, "one@x.io"), f.person(t, "two@x.io")
	tx := begin(t, f.db)
	mustExec(t, tx.Model(&dbUser{}).Where("id = ?", one.ID).Update("email", "taken@x.io"))
	err := whileOpen(t, tx, true, func() error { return f.users.SetEmail(two.ID, "taken@x.io") })
	wantInvalid(t, err, "already used")
}

func TestFindOrCreateByEmailRace(t *testing.T) {
	f := newPeople(t)
	tx := begin(t, f.db)
	other := dbUser{Email: sql.NullString{String: "new@x.io", Valid: true}, CreatedAt: time.Now()}
	mustExec(t, tx.Create(&other))
	var got *User
	err := whileOpen(t, tx, true, func() (err error) {
		got, err = f.users.FindOrCreateByEmail("new@x.io")
		return err
	})
	if err != nil {
		t.Fatalf("FindOrCreateByEmail: %v", err)
	}
	if got.ID != int64(other.ID) {
		t.Errorf("got user %d, want the one the other request made (%d)", got.ID, other.ID)
	}
}

// Accepting an invite creates the person inside its own transaction, so
// finding the other request's row has to work there too.
func TestAcceptInviteWhilePersonCreated(t *testing.T) {
	f, invites := newInvites(t)
	now := time.Now()
	token := mustInvite(t, invites, f.a.ID, "new@x.io", RoleContributor, false, now)
	tx := begin(t, f.db)
	other := dbUser{Email: sql.NullString{String: "new@x.io", Valid: true}, CreatedAt: now}
	mustExec(t, tx.Create(&other))
	var got *User
	err := whileOpen(t, tx, true, func() (err error) {
		got, err = invites.Accept(token, f.a.ID, IdentityClaims{Issuer: "https://iss", Subject: "s1", Email: "new@x.io"}, now)
		return err
	})
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if got.ID != int64(other.ID) {
		t.Errorf("accepted as user %d, want %d", got.ID, other.ID)
	}
	if m, err := f.mems.Get(got.ID, f.a.ID); err != nil || m == nil {
		t.Errorf("membership after accept: %v, %v", m, err)
	}
}

func TestAddMembershipRace(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "m@x.io")
	tx := begin(t, f.db)
	mustExec(t, tx.Create(&dbMembership{UserID: u.ID, SiteID: f.a.ID, Role: RoleContributor, CreatedAt: time.Now()}))
	err := whileOpen(t, tx, true, func() error { return f.mems.Add(u.ID, f.a.ID, RoleAdmin, false) })
	wantInvalid(t, err, "already a member")
}

func TestLinkIdentityRace(t *testing.T) {
	for _, tc := range []struct {
		name    string
		sameOne bool
		want    error
	}{
		{"same person", true, nil},
		{"someone else", false, ErrIdentityConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPeople(t)
			owner, other := f.person(t, "owner@x.io"), f.person(t, "other@x.io")
			linker := other
			if tc.sameOne {
				linker = owner
			}
			tx := begin(t, f.db)
			mustExec(t, tx.Create(&dbIdentity{UserID: owner.ID, Issuer: "https://iss", Subject: "s1", CreatedAt: time.Now()}))
			err := whileOpen(t, tx, true, func() error { return f.ids.Link(linker.ID, "https://iss", "s1", linker.Email) })
			if !errors.Is(err, tc.want) {
				t.Fatalf("Link = %v, want %v", err, tc.want)
			}
		})
	}
}

// A double-submitted invite: the later one replaces the earlier, as an
// invite always replaces an unused one for the same email.
func TestCreateInviteRace(t *testing.T) {
	f, invites := newInvites(t)
	now := time.Now()
	tx := begin(t, f.db)
	mustExec(t, tx.Create(&dbInvite{SiteID: f.a.ID, Email: "inv@x.io", Role: RoleContributor, TokenHash: "other", ExpiresAt: now.Add(InviteTTL), CreatedAt: now}))
	var token string
	err := whileOpen(t, tx, true, func() (err error) {
		_, token, err = invites.Create(f.a.ID, "inv@x.io", RoleContributor, false, 0, now)
		return err
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var hashes []string
	mustExec(t, f.db.Model(&dbInvite{}).Where("email = ? AND used_at IS NULL", "inv@x.io").Pluck("token_hash", &hashes))
	if len(hashes) != 1 || hashes[0] != hashToken(token) {
		t.Errorf("pending invites %v, want only the new one", hashes)
	}
}

func TestCreateSiteRace(t *testing.T) {
	for _, tc := range []struct{ name, key, host, want string }{
		{"key", "c", "other.example.com", "already exists"},
		{"hostname", "d", "c.example.com", "already used"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPeople(t)
			tx := begin(t, f.db)
			mustExec(t, tx.Create(&dbSite{Key: "c", Hostname: "c.example.com", Theme: "default"}))
			err := whileOpen(t, tx, true, func() error {
				return f.sites.Create(&Site{Key: tc.key, Hostname: tc.host, Theme: "default"}, testPlatformHost)
			})
			wantInvalid(t, err, tc.want)
		})
	}
}

func TestUpdateSiteHostnameRace(t *testing.T) {
	f := newPeople(t)
	tx := begin(t, f.db)
	mustExec(t, tx.Create(&dbSite{Key: "c", Hostname: "c.example.com", Theme: "default"}))
	b := *f.b
	b.Hostname = "c.example.com"
	err := whileOpen(t, tx, true, func() error { return f.sites.Update(&b, testPlatformHost) })
	wantInvalid(t, err, "already used")
}

// Post slugs have no pre-check: the unique index is the check.
func TestDuplicateSlugIsAFormError(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	livePost(t, ps, u, "taken", true)
	err := ps.Create(&Post{Slug: "taken", Title: "T", Body: "b", AuthorID: &u.ID})
	wantInvalid(t, err, `"taken"`)

	p := livePost(t, ps, u, "mine", true)
	p.Slug = "taken"
	wantInvalid(t, ps.UpdateContent(p), `"taken"`)
	wantInvalid(t, ps.Update(p), `"taken"`)

	d := &Post{Slug: "draft", Title: "D", Body: "b", AuthorID: &u.ID}
	if err := ps.Create(d); err != nil {
		t.Fatal(err)
	}
	d.Slug = "taken"
	wantInvalid(t, ps.UpdateDraft(d), `"taken"`)
}

// Two posts saved at once with the same new tag share it.
func TestNewTagRace(t *testing.T) {
	f := newPeople(t)
	u := f.person(t, "w@x.io")
	ps := NewPostStore(f.db).ForSite(f.a.ID)
	tx := begin(t, f.db)
	other := dbTag{SiteID: f.a.ID, Name: "fresh"}
	mustExec(t, tx.Create(&other))
	p := &Post{Slug: "tagged", Title: "T", Body: "b", Tags: []string{"fresh"}, AuthorID: &u.ID}
	if err := whileOpen(t, tx, true, func() error { return ps.Create(p) }); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var ids []int64
	mustExec(t, f.db.Table("post_tags").Where("post_id = ?", p.ID).Pluck("tag_id", &ids))
	if len(ids) != 1 || ids[0] != int64(other.ID) {
		t.Errorf("post's tags %v, want the other request's tag %d", ids, other.ID)
	}
}

// A double-clicked settings save: both requests replace the site's
// sections with the same slugs. The later one wins, as it would if they
// ran one after the other.
func TestSetSectionsRace(t *testing.T) {
	f := newPeople(t)
	tx := begin(t, f.db)
	mustExec(t, tx.Create(&dbSection{SiteID: f.a.ID, Slug: "notes", Title: "Other", Vars: "{}"}))
	err := whileOpen(t, tx, true, func() error {
		return f.sites.SetSections(f.a.ID, []Section{{Slug: "notes", Title: "Mine"}})
	})
	if err != nil {
		t.Fatalf("SetSections: %v", err)
	}
	var titles []string
	mustExec(t, f.db.Model(&dbSection{}).Where("site_id = ?", f.a.ID).Pluck("title", &titles))
	if len(titles) != 1 || titles[0] != "Mine" {
		t.Errorf("sections %v, want only the later save", titles)
	}
}
