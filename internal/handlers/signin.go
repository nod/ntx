package handlers

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/nod/ntx/internal/clientip"
	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/people"
	"github.com/nod/ntx/internal/secretbox"
	"github.com/nod/ntx/internal/sso"
)

const flowCookie = "ntx_oauth"

var errSecret = errors.New("the provider's client secret can't be decrypted")

// externalURL is where a path on site is reached from outside: https on
// the site's hostname, or in dev mode this request's scheme and port on the
// site's hostname (dev servers run on a port; r may be on another host,
// such as the platform host showing a site's callback URL).
func (h *Handler) externalURL(r *http.Request, site *models.Site, path string) string {
	if h.d.Dev {
		port := ""
		if _, p, err := net.SplitHostPort(r.Host); err == nil {
			port = ":" + p
		}
		return clientip.Scheme(r) + "://" + site.Hostname + port + path
	}
	return "https://" + site.Hostname + path
}

func (h *Handler) issuer(site *models.Site) string {
	if h.d.IssuerFor != nil {
		return h.d.IssuerFor(site.ProviderType, site.ProviderDomain)
	}
	return sso.Issuer(site.ProviderType, site.ProviderDomain)
}

// ssoSettings builds the site's provider settings for this request.
func (h *Handler) ssoSettings(r *http.Request, site *models.Site) (sso.Settings, error) {
	sealed, err := h.d.Sites.ProviderSecret(site.ID)
	if err != nil {
		return sso.Settings{}, err
	}
	secret, err := secretbox.Open(h.d.EncryptionKey, sealed)
	if err != nil {
		return sso.Settings{}, fmt.Errorf("%w: site %q: %v", errSecret, site.Key, err)
	}
	return sso.Settings{
		Issuer: h.issuer(site), ClientID: site.ProviderClientID, ClientSecret: secret,
		RedirectURL: h.externalURL(r, site, "/auth/callback"),
	}, nil
}

func (h *Handler) setFlowCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name: flowCookie, Value: value, Path: "/auth", MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: !h.d.Dev,
	})
}

// providerTrouble answers a failure to set up or finish a sign-in that is
// the provider's (or its configuration's) fault, not the person's.
func (h *Handler) providerTrouble(w http.ResponseWriter, r *http.Request, err error) {
	log.Printf("sign-in: %v", err)
	switch {
	case errors.Is(err, errSecret):
		h.message(w, r, http.StatusBadGateway, "sign-in unavailable",
			"This site's sign-in provider needs its client secret re-entered by a platform admin.", "", "")
	case errors.Is(err, sso.ErrProvider):
		h.message(w, r, http.StatusBadGateway, "sign-in unavailable",
			"Couldn't reach the sign-in provider. Try again later.", "/admin/login", "back to sign in")
	default:
		h.serverError(w, err)
	}
}

// providerCtx bounds every provider call made for this request, so a hung
// provider still leaves time to answer with the 502 page.
func (h *Handler) providerCtx(r *http.Request) (context.Context, context.CancelFunc) {
	d := h.d.ProviderTimeout
	if d <= 0 {
		d = DefaultProviderTimeout
	}
	return context.WithTimeout(r.Context(), d)
}

func (h *Handler) signInExpired(w http.ResponseWriter, r *http.Request) {
	h.message(w, r, http.StatusBadRequest, "sign-in expired", "Your sign-in expired. Try again.", "/admin/login", "sign in")
}

func (h *Handler) noAccess(w http.ResponseWriter, r *http.Request) {
	h.message(w, r, http.StatusForbidden, "no access", "You don't have access to this site.", "", "")
}

// AuthStart sends the browser to the site's provider. GET from the sign-in
// page; POST (with invite) from an invite page.
func (h *Handler) AuthStart(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		if site.ProviderType == "" {
			h.notFound(w)
			return
		}
		settings, err := h.ssoSettings(r, site)
		if err != nil {
			h.providerTrouble(w, r, err)
			return
		}
		// The invite page posts the token: one in a URL would be logged.
		flow := sso.NewFlow(site.ID, r.PostFormValue("invite"), time.Now())
		ctx, cancel := h.providerCtx(r)
		defer cancel()
		target, err := h.d.SSO.AuthCodeURL(ctx, settings, flow)
		if err != nil {
			h.providerTrouble(w, r, err)
			return
		}
		h.setFlowCookie(w, flow.Encode(h.d.SessionSecret), int(sso.FlowTTL.Seconds()))
		http.Redirect(w, r, target, http.StatusFound)
	})
}

// AuthCallback finishes a sign-in. Every failure leaves no session.
func (h *Handler) AuthCallback(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		h.setFlowCookie(w, "", -1)
		c, err := r.Cookie(flowCookie)
		if err != nil {
			h.signInExpired(w, r)
			return
		}
		now := time.Now()
		flow, err := sso.DecodeFlow(h.d.SessionSecret, c.Value, now)
		if err != nil {
			h.signInExpired(w, r)
			return
		}
		q := r.URL.Query()
		if q.Get("error") != "" {
			// The request log drops this URL's query, so record why the
			// provider refused (no secrets in these two fields).
			log.Printf("sign-in on %q: provider returned error %q: %q", site.Key, q.Get("error"), q.Get("error_description"))
			h.message(w, r, http.StatusBadRequest, "sign-in cancelled", "Sign-in was cancelled.", "/admin/login", "sign in")
			return
		}
		// A flow started on one site can't finish on another.
		if flow.SiteID != site.ID || q.Get("state") == "" || q.Get("state") != flow.State || site.ProviderType == "" {
			h.signInExpired(w, r)
			return
		}
		settings, err := h.ssoSettings(r, site)
		if err != nil {
			h.providerTrouble(w, r, err)
			return
		}
		ctx, cancel := h.providerCtx(r)
		defer cancel()
		claims, err := h.d.SSO.Exchange(ctx, settings, q.Get("code"), flow)
		switch {
		case errors.Is(err, sso.ErrEmailNotVerified):
			h.noAccess(w, r)
			return
		case errors.Is(err, sso.ErrProvider):
			h.providerTrouble(w, r, err)
			return
		case err != nil:
			log.Printf("sign-in on %s: %v", site.Key, err)
			h.signInExpired(w, r)
			return
		}

		user, err := h.d.People.SignIn(site.ID, models.IdentityClaims{
			Issuer: claims.Issuer, Subject: claims.Subject, Email: claims.Email, Name: claims.Name,
		}, flow.Invite, now)
		var mismatch *models.InviteEmailMismatch
		switch {
		case errors.As(err, &mismatch):
			h.message(w, r, http.StatusForbidden, "wrong account",
				fmt.Sprintf("This invite is for %s. Sign in with that account to accept it.", mismatch.Want), "", "")
			return
		case errors.Is(err, models.ErrInviteInvalid):
			h.inviteInvalid(w, r)
			return
		case errors.Is(err, people.ErrNoAccess):
			h.noAccess(w, r)
			return
		case err != nil:
			h.serverError(w, err)
			return
		}
		if err := h.d.Auth.Login(w, user, &site.ID); err != nil {
			h.serverError(w, err)
			return
		}
		h.admin(w, r, "signed_in", nil)
	})
}

func (h *Handler) inviteInvalid(w http.ResponseWriter, r *http.Request) {
	h.message(w, r, http.StatusNotFound, "invite", "This invite link is no longer valid.", "", "")
}

// InvitePage shows an invite and the button that starts accepting it.
func (h *Handler) InvitePage(w http.ResponseWriter, r *http.Request) {
	h.serve(w, r, func(w http.ResponseWriter, r *http.Request, site *models.Site) {
		token := chi.URLParam(r, "token")
		inv, err := h.d.Invites.Lookup(token, site.ID, time.Now())
		if err != nil {
			h.serverError(w, err)
			return
		}
		if inv == nil {
			h.inviteInvalid(w, r)
			return
		}
		if site.ProviderType == "" {
			h.message(w, r, http.StatusOK, "invite", "This site has no sign-in provider yet. Ask whoever invited you to try again later.", "", "")
			return
		}
		h.admin(w, r, "invite", map[string]any{"Invite": inv, "Token": token})
	})
}
