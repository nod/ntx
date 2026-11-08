package sso

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// FlowTTL is how long a sign-in may take between /auth/start and the
// callback.
const FlowTTL = 10 * time.Minute

// ErrFlowInvalid means the flow cookie is missing its signature, was
// tampered with, or has expired. To the person it reads as "your sign-in
// expired".
var ErrFlowInvalid = errors.New("sso: sign-in flow invalid or expired")

// Flow is what /auth/start remembers for the callback, in the signed
// ntx_oauth cookie.
type Flow struct {
	State    string    `json:"s"`
	Nonce    string    `json:"n"`
	Verifier string    `json:"v"`
	SiteID   int64     `json:"site"`
	Invite   string    `json:"inv,omitempty"`
	Expires  time.Time `json:"exp"`
}

func random() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// NewFlow starts a sign-in on siteID, optionally accepting an invite.
func NewFlow(siteID int64, invite string, now time.Time) Flow {
	return Flow{State: random(), Nonce: random(), Verifier: random(), SiteID: siteID, Invite: invite, Expires: now.Add(FlowTTL)}
}

func sign(secret []byte, payload string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Encode serialises and signs the flow for the cookie.
func (f Flow) Encode(secret []byte) string {
	b, _ := json.Marshal(f)
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + sign(secret, payload)
}

// DecodeFlow checks the signature and expiry of a cookie value from Encode.
func DecodeFlow(secret []byte, v string, now time.Time) (Flow, error) {
	payload, sig, ok := strings.Cut(v, ".")
	if !ok || strings.Contains(sig, ".") || !hmac.Equal([]byte(sig), []byte(sign(secret, payload))) {
		return Flow{}, ErrFlowInvalid
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Flow{}, ErrFlowInvalid
	}
	var f Flow
	if err := json.Unmarshal(b, &f); err != nil || !now.Before(f.Expires) {
		return Flow{}, ErrFlowInvalid
	}
	return f, nil
}
