package control

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/isannai/mesh/pkg/auth"
)

// handleAuthVerify validates an EOA-signed message and returns the resolved role.
//
// POST /v1/auth/verify
// Headers:
//   - Authorization: ISANN {sig}
//   - X-ISANN-Message: {role}:{target}:{service}:{nonce}:{expiresAt}:{nodes}
//
// Response: { ok: "true", role: "owner|admin|user", address: "0x..." }
//
// Reference: docs/2026-04-06/broker-auth.md §1-3, mirrors pkg/gate/server.go handleAuthVerify.
func (b *Broker) handleAuthVerify(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-ISANN-Message")
	if r.Method == http.MethodOptions {
		return
	}
	if r.Method != http.MethodPost {
		writeAuthError(w, http.StatusMethodNotAllowed, "POST only")
		return
	}

	sig := strings.TrimPrefix(r.Header.Get("Authorization"), "ISANN ")
	message := r.Header.Get("X-ISANN-Message")
	if sig == "" || message == "" {
		writeAuthError(w, http.StatusUnauthorized, "missing auth headers")
		return
	}

	// Parse message: {role}:{target}:{service}:{nonce}:{expiresAt}:{nodes}
	parts := strings.SplitN(message, ":", 6)
	if len(parts) < 5 {
		writeAuthError(w, http.StatusUnauthorized, "invalid message format")
		return
	}
	if parts[1] != "control" && parts[1] != "broker" {
		writeAuthError(w, http.StatusUnauthorized, "invalid target: expected control")
		return
	}
	expiresAt, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil || time.Now().Unix() > expiresAt {
		writeAuthError(w, http.StatusUnauthorized, "signature expired")
		return
	}

	address, err := auth.RecoverAddress(message, sig)
	if err != nil {
		writeAuthError(w, http.StatusUnauthorized, "invalid signature")
		return
	}

	role := classifyAddress(address, b.Auth.Owner, b.Auth.Admins, b.Auth.Users)
	if role == "" {
		// Broker is open: any signed-in wallet becomes a regular user.
		role = "user"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"ok":      "true",
		"role":    role,
		"address": address,
	})
}
