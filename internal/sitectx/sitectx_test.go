package sitectx

import (
	"net/http/httptest"
	"testing"

	"github.com/nod/ntx/internal/models"
)

func TestSiteRoundTrip(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if From(r) != nil {
		t.Fatal("a plain request has no site")
	}
	site := &models.Site{ID: 3, Key: "a"}
	if got := From(With(r, site)); got != site {
		t.Errorf("From(With(r, site)) = %v", got)
	}
}

func TestPlatformFlag(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	if IsPlatform(r) {
		t.Fatal("a plain request is not on the platform host")
	}
	if !IsPlatform(WithPlatform(r)) {
		t.Error("WithPlatform must set the flag")
	}
}
