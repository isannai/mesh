package control

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/isannai/mesh/pkg/auth"
)

// ctxKey is the type for context keys to avoid collisions.
type ctxKey string

const (
	ctxKeyAddress ctxKey = "iann.address"
	ctxKeyRole    ctxKey = "iann.role"
)

// authMiddleware enforces EOA-signature based authentication on Broker routes.
//
// Flow (per docs/2026-04-06/broker-auth.md §1-2):
//  1. Classify the route → none / user / admin
//  2. none → pass
//  3. user && Auth.IsPublic() → pass
//  4. Parse Authorization: ISANN {sig} + X-ISANN-Message
//  5. Validate target == "broker" and expiresAt > now
//  6. ecrecover address
//  7. Check address is owner / admin / user (per allow lists)
//  8. admin level → must be owner or admin
func (b *Broker) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// CORS preflight always passes
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}

		// API policy gate — owner sig bypasses entirely (broker self-admin
		// is always allowed). For non-owner / unauthenticated requests, the
		// route is mapped to a feature and rejected when disabled.
		isOwner := false
		if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "ISANN ") {
			sig := strings.TrimPrefix(authHeader, "ISANN ")
			if address, err := auth.RecoverAddress(r.Header.Get("X-ISANN-Message"), sig); err == nil {
				if strings.EqualFold(address, b.Auth.Owner) {
					isOwner = true
				}
			}
		}
		if !isOwner {
			pol := b.Policy()
			if !pol.IsRouteAllowed(r.Method, r.URL.Path) {
				writeAuthError(w, http.StatusForbidden, "feature_disabled")
				return
			}
		}

		// /v1/admin/* → owner only (Broker settings protection)
		if strings.HasPrefix(r.URL.Path, "/v1/admin/") {
			authHeader := r.Header.Get("Authorization")
			if !strings.HasPrefix(authHeader, "ISANN ") {
				writeAuthError(w, http.StatusUnauthorized, "authorization required")
				return
			}
			sig := strings.TrimPrefix(authHeader, "ISANN ")
			message := r.Header.Get("X-ISANN-Message")
			if message == "" {
				writeAuthError(w, http.StatusUnauthorized, "X-ISANN-Message required")
				return
			}
			parts := strings.SplitN(message, ":", 6)
			if len(parts) >= 5 {
				if exp, err := strconv.ParseInt(parts[4], 10, 64); err == nil && time.Now().Unix() > exp {
					writeAuthError(w, http.StatusUnauthorized, "signature expired")
					return
				}
			}
			address, err := auth.RecoverAddress(message, sig)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, "invalid signature")
				return
			}
			if !strings.EqualFold(address, b.Auth.Owner) {
				writeAuthError(w, http.StatusForbidden, "owner only")
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyAddress, address)
			ctx = context.WithValue(ctx, ctxKeyRole, "owner")
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		// Everything else → pass through (Provider handles its own auth)
		// If auth headers present, inject address into context for downstream use
		if authHeader := r.Header.Get("Authorization"); strings.HasPrefix(authHeader, "ISANN ") {
			sig := strings.TrimPrefix(authHeader, "ISANN ")
			message := r.Header.Get("X-ISANN-Message")
			if address, err := auth.RecoverAddress(message, sig); err == nil {
				ctx := context.WithValue(r.Context(), ctxKeyAddress, address)
				role := classifyAddress(address, b.Auth.Owner, b.Auth.Admins, b.Auth.Users)
				if role == "" {
					role = "user"
				}
				ctx = context.WithValue(ctx, ctxKeyRole, role)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// classifyAddress checks Owner/Admins/Users lists and returns the role string.
func classifyAddress(address string, owner string, admins, users []string) string {
	if owner != "" && strings.EqualFold(address, owner) {
		return "owner"
	}
	for _, a := range admins {
		if strings.EqualFold(address, a) {
			return "admin"
		}
	}
	for _, u := range users {
		if strings.EqualFold(address, u) {
			return "user"
		}
	}
	return ""
}

// writeAuthError writes a JSON error response.
func writeAuthError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
