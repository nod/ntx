package models

import "testing"

func TestEnsureRootIdempotent(t *testing.T) {
	d := newTestSiteDB(t)
	users := NewUserStore(d)
	first, err := users.EnsureRoot()
	if err != nil {
		t.Fatal(err)
	}
	second, err := users.EnsureRoot()
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("EnsureRoot must return the same root user; got %+v and %+v", first, second)
	}
	if !first.IsRoot || first.Email != "" {
		t.Errorf("root must have is_root and no email, got %+v", first)
	}
	var nullEmail int64
	if err := d.Raw("SELECT COUNT(*) FROM users WHERE is_root AND email IS NULL").Scan(&nullEmail).Error; err != nil {
		t.Fatal(err)
	}
	if nullEmail != 1 {
		t.Error("root's email must be stored as NULL")
	}
	got, err := users.GetByID(first.ID)
	if err != nil || got == nil || !got.IsRoot {
		t.Errorf("GetByID(root) = %+v, %v", got, err)
	}
	missing, err := users.GetByID(9999)
	if err != nil || missing != nil {
		t.Errorf("GetByID(missing) = %+v, %v; want nil, nil", missing, err)
	}
}
