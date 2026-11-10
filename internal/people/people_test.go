package people

import (
	"errors"
	"testing"
	"time"

	"github.com/nod/ntx/internal/db/dbtest"
	"github.com/nod/ntx/internal/models"
)

const iss = "https://idp.example/"

type fx struct {
	svc   *Service
	users *models.UserStore
	ids   *models.IdentityStore
	mems  *models.MembershipStore
	inv   *models.InviteStore
	a, b  *models.Site
}

func setup(t *testing.T) *fx {
	t.Helper()
	d := dbtest.Open(t)
	f := &fx{users: models.NewUserStore(d), ids: models.NewIdentityStore(d), mems: models.NewMembershipStore(d), inv: models.NewInviteStore(d)}
	f.svc = New(f.users, f.ids, f.mems, f.inv)
	if _, err := f.users.EnsureRoot(); err != nil {
		t.Fatal(err)
	}
	sites := models.NewSiteStore(d)
	f.a = &models.Site{Key: "a", Hostname: "a.example.com", Theme: "t"}
	f.b = &models.Site{Key: "b", Hostname: "b.example.com", Theme: "t"}
	for _, s := range []*models.Site{f.a, f.b} {
		if err := sites.Create(s, "admin.example.com"); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func (f *fx) add(t *testing.T, site *models.Site, email, role string) *models.User {
	t.Helper()
	u, err := f.svc.AddMember(site.ID, email, role, false)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func cl(sub, email string) models.IdentityClaims {
	return models.IdentityClaims{Issuer: iss, Subject: sub, Email: email, Name: "From Provider"}
}

func TestSignInKnownIdentity(t *testing.T) {
	f := setup(t)
	u := f.add(t, f.a, "k@x.io", models.RoleContributor)
	if err := f.ids.Link(u.ID, iss, "s1", u.Email); err != nil {
		t.Fatal(err)
	}
	// The provider now reports another email: the identity still decides.
	got, err := f.svc.SignIn(f.a.ID, cl("s1", "changed@x.io"), "", time.Now())
	if err != nil || got.ID != u.ID {
		t.Fatalf("SignIn = %+v, %v", got, err)
	}
	if again, _ := f.users.GetByID(u.ID); again.Email != "k@x.io" {
		t.Fatalf("email changed to %q; providers must never update it", again.Email)
	}
}

func TestSignInEmailMatchLinks(t *testing.T) {
	f := setup(t)
	u := f.add(t, f.a, "m@x.io", models.RoleContributor)
	got, err := f.svc.SignIn(f.a.ID, cl("s1", "M@x.io"), "", time.Now())
	if err != nil || got.ID != u.ID {
		t.Fatalf("SignIn = %+v, %v", got, err)
	}
	if id, _ := f.ids.Find(iss, "s1"); id == nil || id.UserID != u.ID {
		t.Fatalf("identity not linked: %+v", id)
	}
}

func TestSignInEmailMatchNeedsMembershipHere(t *testing.T) {
	f := setup(t)
	f.add(t, f.b, "m@x.io", models.RoleAdmin)
	if _, err := f.svc.SignIn(f.a.ID, cl("s1", "m@x.io"), "", time.Now()); !errors.Is(err, ErrNoAccess) {
		t.Fatalf("SignIn on a site they don't belong to = %v, want ErrNoAccess", err)
	}
	if id, _ := f.ids.Find(iss, "s1"); id != nil {
		t.Fatal("an identity was linked by a sign-in that was refused")
	}
}

func TestSignInUnknownGives403Error(t *testing.T) {
	f := setup(t)
	if _, err := f.svc.SignIn(f.a.ID, cl("s1", "nobody@x.io"), "", time.Now()); !errors.Is(err, ErrNoAccess) {
		t.Fatalf("unknown person = %v, want ErrNoAccess", err)
	}
}

func TestSignInReassignedIsNoAccess(t *testing.T) {
	f := setup(t)
	u := f.add(t, f.a, "m@x.io", models.RoleContributor)
	if err := f.ids.Link(u.ID, iss, "old-sub", u.Email); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SignIn(f.a.ID, cl("new-sub", "m@x.io"), "", time.Now()); !errors.Is(err, ErrNoAccess) {
		t.Fatalf("second subject at the same issuer = %v, want ErrNoAccess", err)
	}
}

func TestSignInKnownIdentityWithoutMembership(t *testing.T) {
	f := setup(t)
	u := f.add(t, f.b, "m@x.io", models.RoleContributor)
	if err := f.ids.Link(u.ID, iss, "s1", u.Email); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.SignIn(f.a.ID, cl("s1", "m@x.io"), "", time.Now()); !errors.Is(err, ErrNoAccess) {
		t.Fatalf("known identity, no membership here = %v, want ErrNoAccess", err)
	}
}

func TestSignInRefreshesName(t *testing.T) {
	f := setup(t)
	u := f.add(t, f.a, "m@x.io", models.RoleContributor)
	got, err := f.svc.SignIn(f.a.ID, cl("s1", "m@x.io"), "", time.Now())
	if err != nil || got.Name != "From Provider" {
		t.Fatalf("SignIn = %+v, %v", got, err)
	}
	if stored, _ := f.users.GetByID(u.ID); stored.Name != "From Provider" {
		t.Fatalf("stored name = %q", stored.Name)
	}
	nameless := cl("s1", "m@x.io")
	nameless.Name = ""
	if got, _ := f.svc.SignIn(f.a.ID, nameless, "", time.Now()); got.Name != "From Provider" {
		t.Fatalf("an empty name claim cleared the name: %q", got.Name)
	}
}

func TestSignInNeverRoot(t *testing.T) {
	f := setup(t)
	if _, err := f.svc.SignIn(f.a.ID, cl("s1", ""), "", time.Now()); !errors.Is(err, ErrNoAccess) {
		t.Fatalf("empty email = %v, want ErrNoAccess", err)
	}
}

func TestSignInWithInvite(t *testing.T) {
	f := setup(t)
	now := time.Now()
	_, token, err := f.inv.Create(f.a.ID, "new@x.io", models.RoleAdmin, false, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	var mm *models.InviteEmailMismatch
	if _, err := f.svc.SignIn(f.a.ID, cl("s1", "wrong@x.io"), token, now); !errors.As(err, &mm) {
		t.Fatalf("wrong account = %v, want InviteEmailMismatch", err)
	}
	u, err := f.svc.SignIn(f.a.ID, cl("s1", "new@x.io"), token, now)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := f.mems.Get(u.ID, f.a.ID); m == nil || m.Role != models.RoleAdmin {
		t.Fatalf("membership = %+v", m)
	}
	if _, err := f.svc.SignIn(f.a.ID, cl("s1", "new@x.io"), token, now); !errors.Is(err, models.ErrInviteInvalid) {
		t.Fatalf("reused invite = %v, want ErrInviteInvalid", err)
	}
}

func TestAddMemberCancelsPendingInvite(t *testing.T) {
	f := setup(t)
	now := time.Now()
	if _, _, err := f.inv.Create(f.a.ID, "p@x.io", models.RoleContributor, false, 0, now); err != nil {
		t.Fatal(err)
	}
	f.add(t, f.a, "P@x.io", models.RoleAdmin)
	if p, _ := f.inv.ListPending(f.a.ID, now); len(p) != 0 {
		t.Fatalf("pending invites after AddMember = %+v", p)
	}
	if _, err := f.svc.AddMember(f.a.ID, "p@x.io", models.RoleAdmin, false); err == nil {
		t.Fatal("adding an existing member succeeded")
	}
}

// A bad role is refused before anyone is created, so a forged form value
// can't leave behind a person who belongs to no site.
func TestAddMemberBadRoleCreatesNoOne(t *testing.T) {
	f := setup(t)
	if _, err := f.svc.AddMember(f.a.ID, "new@x.io", "owner", false); err == nil {
		t.Fatal(`AddMember accepted role "owner"`)
	}
	if u, _ := f.users.FindByEmail("new@x.io"); u != nil {
		t.Fatalf("a refused add created %+v", u)
	}
}
