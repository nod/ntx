package models

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func newInvites(t *testing.T) (*peopleFixture, *InviteStore) {
	t.Helper()
	f := newPeople(t)
	return f, NewInviteStore(f.db)
}

func mustInvite(t *testing.T, s *InviteStore, siteID int64, email, role string, canPublish bool, now time.Time) string {
	t.Helper()
	_, token, err := s.Create(siteID, email, role, canPublish, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func claims(sub, email string) IdentityClaims {
	return IdentityClaims{Issuer: "https://idp.example/", Subject: sub, Email: email, Name: "N"}
}

func TestInviteEmailNormalised(t *testing.T) {
	f, s := newInvites(t)
	now := time.Now()
	inv, token, err := s.Create(f.a.ID, " Alice@Example.COM ", RoleContributor, false, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Email != "alice@example.com" {
		t.Fatalf("stored email = %q", inv.Email)
	}
	u, err := s.Accept(token, f.a.ID, claims("s1", "alice@example.com"), now)
	if err != nil || u == nil || u.Email != "alice@example.com" {
		t.Fatalf("Accept = %+v, %v", u, err)
	}
}

func TestInviteReplacesPending(t *testing.T) {
	f, s := newInvites(t)
	now := time.Now()
	first := mustInvite(t, s, f.a.ID, "a@x.io", RoleContributor, false, now)
	mustInvite(t, s, f.a.ID, "a@x.io", RoleAdmin, false, now)
	if inv, _ := s.Lookup(first, f.a.ID, now); inv != nil {
		t.Fatal("the replaced invite still looks up")
	}
	pending, err := s.ListPending(f.a.ID, now)
	if err != nil || len(pending) != 1 || pending[0].Role != RoleAdmin {
		t.Fatalf("ListPending = %+v, %v", pending, err)
	}
}

func TestInviteExpiredDoesNotBlock(t *testing.T) {
	f, s := newInvites(t)
	now := time.Now()
	old := mustInvite(t, s, f.a.ID, "a@x.io", RoleContributor, false, now)
	later := now.Add(8 * 24 * time.Hour)
	if inv, _ := s.Lookup(old, f.a.ID, later); inv != nil {
		t.Fatal("an 8-day-old invite still looks up")
	}
	if _, _, err := s.Create(f.a.ID, "a@x.io", RoleContributor, false, 0, later); err != nil {
		t.Fatalf("Create after expiry = %v", err)
	}
}

func TestInviteRejectsExistingMember(t *testing.T) {
	f, s := newInvites(t)
	u := f.person(t, "m@x.io")
	if err := f.mems.Add(u.ID, f.a.ID, RoleContributor, false); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.Create(f.a.ID, "M@x.io", RoleAdmin, false, 0, time.Now())
	if !isValidation(err) || !strings.Contains(err.Error(), "already a member") {
		t.Fatalf("Create for a member = %v", err)
	}
	if _, _, err := s.Create(f.a.ID, "not-an-email", RoleAdmin, false, 0, time.Now()); !isValidation(err) {
		t.Fatalf("Create with a bad email = %v", err)
	}
	if _, _, err := s.Create(f.a.ID, "z@x.io", "owner", false, 0, time.Now()); !isValidation(err) {
		t.Fatalf("Create with a bad role = %v", err)
	}
}

func TestInviteAcceptCreatesUserMembershipIdentity(t *testing.T) {
	f, s := newInvites(t)
	now := time.Now()
	token := mustInvite(t, s, f.a.ID, "a@x.io", RoleContributor, true, now)
	u, err := s.Accept(token, f.a.ID, claims("s1", "a@x.io"), now)
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "a@x.io" {
		t.Fatalf("user = %+v", u)
	}
	m, _ := f.mems.Get(u.ID, f.a.ID)
	if m == nil || m.Role != RoleContributor || !m.CanPublish {
		t.Fatalf("membership = %+v", m)
	}
	if id, _ := f.ids.Find("https://idp.example/", "s1"); id == nil || id.UserID != u.ID {
		t.Fatalf("identity = %+v", id)
	}
	if _, err := s.Accept(token, f.a.ID, claims("s1", "a@x.io"), now); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("second Accept = %v, want ErrInviteInvalid", err)
	}
}

func TestInviteAcceptEmailMismatch(t *testing.T) {
	f, s := newInvites(t)
	now := time.Now()
	token := mustInvite(t, s, f.a.ID, "a@x.io", RoleContributor, false, now)
	_, err := s.Accept(token, f.a.ID, claims("s1", "b@x.io"), now)
	var mm *InviteEmailMismatch
	if !errors.As(err, &mm) || mm.Want != "a@x.io" {
		t.Fatalf("Accept with another email = %v", err)
	}
	if inv, _ := s.Lookup(token, f.a.ID, now); inv == nil {
		t.Fatal("a mismatched Accept used up the invite")
	}
}

func TestInviteAcceptKnownIdentityDifferentEmail(t *testing.T) {
	f, s := newInvites(t)
	now := time.Now()
	c := f.person(t, "c@x.io")
	if err := f.ids.Link(c.ID, "https://idp.example/", "s1", c.Email); err != nil {
		t.Fatal(err)
	}
	token := mustInvite(t, s, f.a.ID, "a@x.io", RoleContributor, false, now)
	_, err := s.Accept(token, f.a.ID, claims("s1", "a@x.io"), now)
	var mm *InviteEmailMismatch
	if !errors.As(err, &mm) || mm.Want != "a@x.io" {
		t.Fatalf("Accept by an identity linked to c@x.io = %v", err)
	}
	if inv, _ := s.Lookup(token, f.a.ID, now); inv == nil {
		t.Fatal("the refused Accept used up the invite")
	}
}

func TestInviteAcceptReassignedGuard(t *testing.T) {
	f, s := newInvites(t)
	now := time.Now()
	a := f.person(t, "a@x.io")
	if err := f.ids.Link(a.ID, "https://idp.example/", "sub1", a.Email); err != nil {
		t.Fatal(err)
	}
	token := mustInvite(t, s, f.a.ID, "a@x.io", RoleContributor, false, now)
	if _, err := s.Accept(token, f.a.ID, claims("sub2", "a@x.io"), now); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("Accept with a second subject = %v, want ErrIdentityConflict", err)
	}
	if inv, _ := s.Lookup(token, f.a.ID, now); inv == nil {
		t.Fatal("the transaction didn't roll back: the invite is used")
	}
	if m, _ := f.mems.Get(a.ID, f.a.ID); m != nil {
		t.Fatal("a membership was created despite the conflict")
	}
}

func TestInviteAcceptConcurrent(t *testing.T) {
	f, s := newInvites(t)
	now := time.Now()
	token := mustInvite(t, s, f.a.ID, "a@x.io", RoleContributor, false, now)
	var wg sync.WaitGroup
	results := make([]error, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, results[i] = s.Accept(token, f.a.ID, claims("s1", "a@x.io"), now)
		}(i)
	}
	wg.Wait()
	ok, invalid := 0, 0
	for _, err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrInviteInvalid):
			invalid++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || invalid != 1 {
		t.Fatalf("ok=%d invalid=%d, want exactly one success", ok, invalid)
	}
}

func TestInviteOtherSite(t *testing.T) {
	f, s := newInvites(t)
	now := time.Now()
	token := mustInvite(t, s, f.a.ID, "a@x.io", RoleContributor, false, now)
	if inv, _ := s.Lookup(token, f.b.ID, now); inv != nil {
		t.Fatal("site A's invite looked up on site B")
	}
	if _, err := s.Accept(token, f.b.ID, claims("s1", "a@x.io"), now); !errors.Is(err, ErrInviteInvalid) {
		t.Fatalf("Accept on site B = %v", err)
	}
	inv, _ := s.Lookup(token, f.a.ID, now)
	if err := s.Cancel(inv.ID, f.b.ID); err == nil {
		t.Fatal("Cancel through site B succeeded")
	}
	if _, err := s.Regenerate(inv.ID, f.b.ID, now); err == nil {
		t.Fatal("Regenerate through site B succeeded")
	}
}

func TestInviteAcceptExistingMembershipKept(t *testing.T) {
	f, s := newInvites(t)
	now := time.Now()
	token := mustInvite(t, s, f.a.ID, "a@x.io", RoleAdmin, false, now)
	u := f.person(t, "a@x.io")
	if err := f.mems.Add(u.ID, f.a.ID, RoleContributor, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(token, f.a.ID, claims("s1", "a@x.io"), now); err != nil {
		t.Fatal(err)
	}
	if m, _ := f.mems.Get(u.ID, f.a.ID); m.Role != RoleContributor {
		t.Fatalf("role = %q, want contributor kept", m.Role)
	}
	if inv, _ := s.Lookup(token, f.a.ID, now); inv != nil {
		t.Fatal("invite not marked used")
	}
}

func TestInviteRegenerateAndCancel(t *testing.T) {
	f, s := newInvites(t)
	now := time.Now()
	old := mustInvite(t, s, f.a.ID, "a@x.io", RoleContributor, false, now)
	inv, _ := s.Lookup(old, f.a.ID, now)
	fresh, err := s.Regenerate(inv.ID, f.a.ID, now.Add(6*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Lookup(old, f.a.ID, now); got != nil {
		t.Fatal("the old token still works after Regenerate")
	}
	if got, _ := s.Lookup(fresh, f.a.ID, now.Add(12*24*time.Hour)); got == nil {
		t.Fatal("Regenerate didn't extend the expiry")
	}
	if err := s.Cancel(inv.ID, f.a.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Lookup(fresh, f.a.ID, now); got != nil {
		t.Fatal("cancelled invite still looks up")
	}
	mustInvite(t, s, f.a.ID, "b@x.io", RoleContributor, false, now)
	if err := s.CancelPendingFor(f.a.ID, "B@x.io"); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.ListPending(f.a.ID, now); len(p) != 0 {
		t.Fatalf("pending after CancelPendingFor = %+v", p)
	}
}
