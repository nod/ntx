package sitectx

import (
	"net/http/httptest"
	"testing"

	"github.com/nod/ntx/internal/config"
)

func TestWithAndFrom(t *testing.T) {
	site := &config.Site{Key: "mysite"}
	r := httptest.NewRequest("GET", "/", nil)

	if got := From(r); got != nil {
		t.Errorf("expected nil before With, got %+v", got)
	}

	r = With(r, site)
	got := From(r)
	if got != site {
		t.Errorf("expected %+v, got %+v", site, got)
	}
}
