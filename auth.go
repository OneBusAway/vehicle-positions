package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

type contextKey string

const claimsKey contextKey = "claims"

// The role vocabulary. A JWT's "role" claim, a stored user's role column and
// every role check in the server all draw on these three names, so a role
// cannot be spelled one way in one place and another elsewhere.
const (
	roleDriver = "driver"
	roleAdmin  = "admin"
	roleRider  = "rider"
)

// staffRoles are the roles a member of staff may hold: the ones the user form
// offers, and the ones the staff API admits. A rider is deliberately not one
// of them — a rider token is signed with the same secret and would otherwise
// validate on staff routes.
var staffRoles = []string{roleDriver, roleAdmin}

// contextWithClaims stores validated JWT claims on the context. Shared by
// requireAuth and requireAdminPage so both middlewares wire claims the same
// way.
func contextWithClaims(ctx context.Context, claims jwt.MapClaims) context.Context {
	return context.WithValue(ctx, claimsKey, claims)
}

const bcryptCost = bcrypt.DefaultCost

// sessionCookieName is the cookie holding the admin UI's browser session
// JWT. requireAuth falls back to it only when the Authorization header is
// entirely absent (see requireAuth). Task 7's session helpers reuse it.
const sessionCookieName = "vp_session"

var dummyHash []byte

func init() {
	// Generate a valid hash at startup using the central cost.
	// This ensures our timing side-channel prevention always matches the real hashing time.
	var err error
	dummyHash, err = bcrypt.GenerateFromPassword([]byte("dummy"), bcryptCost)
	if err != nil {
		panic("failed to generate dummy hash at startup: " + err.Error())
	}
}

// LoginRequest is the JSON payload for POST /api/v1/auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse is returned by a successful login and by the refresh
// endpoint, so a client can parse both with one model.
//
// Token repeats AccessToken verbatim. It is the field the original
// single-token response returned and the field every current client reads:
// the Android app declares it non-nullable (`LoginResponse(val token:
// String)`), so dropping it would not degrade to an empty string, it would
// raise MissingFieldException on the next login. It is deprecated, not
// supported — new clients should read access_token and refresh before
// expires_in elapses.
//
// TODO: remove the deprecated "token" alias once the Android app and the
// simulator read access_token (target: the Milestone 3 release).
type LoginResponse struct {
	Token        string `json:"token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

// UserFetcher is the store interface needed by the login handler.
type UserFetcher interface {
	GetUserByEmail(ctx context.Context, email string) (*User, error)
}

// handleLogin returns the JSON API login handler. limiter may be nil (e.g. in
// tests that don't exercise rate limiting); trustProxy controls which IP
// clientIP() reports to the limiter. When present, the rate-limit check runs
// before the store is touched.
func handleLogin(fetcher UserFetcher, refreshTokens RefreshTokenCreator, secret []byte, ttls tokenTTLs, limiter *LoginRateLimiter, trustProxy bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<10)

		var req LoginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
			return
		}

		if req.Email == "" || req.Password == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email and password are required"})
			return
		}

		if limiter != nil && !limiter.Allow(clientIP(r, trustProxy), req.Email) {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts"})
			return
		}

		user, err := fetcher.GetUserByEmail(r.Context(), req.Email)
		if err != nil {
			if errors.Is(err, ErrUserNotFound) {
				_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password)) // timing side-channel prevention
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
				return
			}
			slog.Error("login: database error", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		err = bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password))
		if err != nil {
			if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
				return
			}
			slog.Error("login: bcrypt error", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		if !user.Active {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
			return
		}

		// Successful authentication: clear the per-email rate-limit window so
		// legitimate repeat logins aren't counted toward the brute-force budget.
		if limiter != nil {
			limiter.ResetEmail(req.Email)
		}

		accessToken, err := generateJWT(user, secret, ttls.access)
		if err != nil {
			slog.Error("token generation failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		// A login that returned an access token but no refresh token would
		// leave the client with a credential it cannot renew, and it would
		// find that out only once the access token expired. Fail the login
		// instead.
		refreshToken, err := issueRefreshToken(r.Context(), refreshTokens, user.ID, ttls.refresh)
		if err != nil {
			slog.Error("login: failed to issue refresh token", "sub", user.ID, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		writeJSON(w, http.StatusOK, newLoginResponse(accessToken, refreshToken, ttls.access))
	}
}

// sessionLifetime is how long the admin UI's browser session JWT stays valid.
// The browser has no refresh flow — the cookie is the whole session — so it
// keeps the 24 hours it has always had rather than following the API access
// token TTL. It also bounds how long a revocation row has to be honoured (see
// revoked_tokens.expires_at).
const sessionLifetime = 24 * time.Hour

const (
	// defaultAccessTokenTTL stays at the 24 hours main already issued. The
	// refresh endpoint ships in this change, but the Android app cannot use
	// it yet: TripReporter treats a 401 as AUTH_EXPIRED and stops reporting
	// until the driver signs in again, so a short default would end every
	// driver's location reports a few minutes into a shift. Deployments that
	// have a client able to refresh can set ACCESS_TOKEN_TTL=15m today.
	//
	// TODO: drop this to 15 * time.Minute in the PR that teaches the Android
	// client to refresh on 401 — that is the change that makes a short
	// access token safe to default to.
	defaultAccessTokenTTL  = 24 * time.Hour
	defaultRefreshTokenTTL = 7 * 24 * time.Hour
)

// tokenTTLs carries the two configurable lifetimes together. Passing one
// value rather than two positional durations makes them impossible to swap at
// a call site.
type tokenTTLs struct {
	access  time.Duration
	refresh time.Duration
}

// validateTokenTTLs rejects configurations that cannot work: a non-positive
// access TTL issues tokens that are already expired, and a refresh TTL no
// longer than the access TTL leaves nothing to refresh with.
func validateTokenTTLs(ttls tokenTTLs) error {
	if ttls.access <= 0 {
		return fmt.Errorf("ACCESS_TOKEN_TTL must be positive, got %s", ttls.access)
	}
	if ttls.refresh <= ttls.access {
		return fmt.Errorf("REFRESH_TOKEN_TTL (%s) must be longer than ACCESS_TOKEN_TTL (%s)", ttls.refresh, ttls.access)
	}
	return nil
}

// newJTI returns a random 128-bit token identifier, hex-encoded. It must come
// from crypto/rand rather than math/rand or a counter: a guessable jti would
// let an attacker pre-emptively revoke other users' tokens.
func newJTI() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate jti: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// generateJWT creates a signed JWT valid for ttl. It is the only path that
// issues session tokens — the JSON API login, the refresh endpoint, and the
// admin UI's form login all call it — so every token carries a jti and can be
// revoked. The TTL is a parameter because those callers no longer agree on
// one: API access tokens are short and renewable, the admin UI's cookie
// session is not (see sessionLifetime).
func generateJWT(user *User, secret []byte, ttl time.Duration) (string, error) {
	now := time.Now()

	jti, err := newJTI()
	if err != nil {
		return "", err
	}

	claims := jwt.MapClaims{
		"sub":   fmt.Sprintf("%d", user.ID),
		"email": user.Email,
		"role":  user.Role,
		"jti":   jti,
		"exp":   now.Add(ttl).Unix(),
		"iat":   now.Unix(),
		"iss":   "vehicle-positions-api",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// requireAdmin is middleware that restricts access to admin-role users.
// It must be chained after requireAuth, which sets JWT claims on the context.
func requireAdmin() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := r.Context().Value(claimsKey).(jwt.MapClaims)
			if !ok {
				slog.Warn("requireAdmin: claims missing from context")
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}

			role, ok := claims["role"].(string)
			if !ok || role != roleAdmin {
				slog.Warn("requireAdmin: access denied",
					"sub", claims["sub"],
					"role", claims["role"],
					"path", r.URL.Path,
				)
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin access required"})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// parseSessionToken validates an HS256 session JWT (algorithm, issuer,
// expiry) and returns its claims. It is the single validation path shared by
// the API middleware and the admin UI's cookie session (adminClaimsFromCookie),
// so changes to token validation cannot silently diverge between the two.
//
// It deliberately performs no I/O: revocation is a separate step (checkRevoked)
// that both callers invoke, so a database dependency never has to be threaded
// through JWT parsing.
//
// WithExpirationRequired rejects a signed token that carries no exp claim.
// generateJWT always sets one, and a revocation row needs the expiry to record
// expires_at, so a token without exp is not one this server issued.
func parseSessionToken(tokenString string, secret []byte) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	}, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("vehicle-positions-api"),
		jwt.WithExpirationRequired())
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, errors.New("token marked invalid")
	}
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid token claims")
	}
	return claims, nil
}

// checkRevoked reports whether the token behind claims has been logged out.
// It is the second half of validation, kept out of parseSessionToken so that
// function stays pure; both token paths (the API's Authorization header and
// the admin UI's vp_session cookie) must call it, and
// TestAdminCookiePath_RejectsRevokedToken pins that they do.
func checkRevoked(ctx context.Context, claims jwt.MapClaims, checker TokenChecker) (bool, error) {
	jti, _ := claims["jti"].(string)
	if jti == "" {
		// Intentional backwards compatibility: tokens issued before jti
		// existed carry no identifier to revoke, so they are accepted rather
		// than logging every existing session out on deploy. They are also
		// permanently unrevokable, which is why this is a warning — every
		// token issued from here on has a jti and tokens live sessionLifetime,
		// so this should stop appearing within a day of deploying.
		// TODO: drop this shim and reject tokens without a jti once all
		// pre-revocation tokens have expired (sessionLifetime after deploy).
		// generateRiderJWT issues no jti, so a rider token reaching here is
		// the normal case rather than a leftover — warning per request would
		// bury the staff signal under the rider API's upload volume. Rider
		// sessions are consequently not revocable; there is no rider logout
		// to revoke through today.
		// TODO: give rider tokens a jti and drop this exemption when the
		// rider API grows a sign-out.
		if role, _ := claims["role"].(string); role != roleRider {
			slog.Warn("accepted token without jti; it cannot be revoked", "sub", claims["sub"])
		}
		return false, nil
	}
	return checker.IsTokenRevoked(ctx, jti)
}

// requireAuth is middleware that validates the Bearer JWT on the staff API. A
// rider token is signed with the same secret and would otherwise validate
// here, so the role is checked at the door rather than at each handler.
// checker is consulted for every validated token so a logged-out one is
// rejected for the rest of its lifetime.
func requireAuth(secret []byte, checker TokenChecker) func(http.Handler) http.Handler {
	return requireRoles(secret, checker, true, staffRoles...)
}

// requireRoles returns middleware that validates the Bearer JWT and admits
// only the named roles, storing the claims on the request context. When
// allowCookie is set, a request with no Authorization header at all falls back
// to the admin UI's browser session cookie (spec §4.2); a present-but-bad
// header never falls back, and a client that has no browser session — the
// rider API — never accepts a cookie at all.
func requireRoles(secret []byte, checker TokenChecker, allowCookie bool, roles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			var tokenString string
			switch {
			case authHeader == "" && allowCookie:
				c, err := r.Cookie(sessionCookieName)
				if err != nil || c.Value == "" {
					writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid authorization header"})
					return
				}
				tokenString = c.Value
			case strings.HasPrefix(authHeader, "Bearer "):
				tokenString = strings.TrimPrefix(authHeader, "Bearer ")
			default:
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid authorization header"})
				return
			}

			claims, err := parseSessionToken(tokenString, secret)
			if err != nil {
				slog.Warn("token validation failed", "error", err)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
				return
			}
			if role, _ := claims["role"].(string); !slices.Contains(roles, role) {
				slog.Warn("role not permitted",
					"sub", claims["sub"],
					"role", claims["role"],
					"allowed", roles,
					"path", r.URL.Path,
				)
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
				return
			}
			revoked, err := checkRevoked(r.Context(), claims, checker)
			if err != nil {
				// Fail closed. A rate limiter that can't decide should let
				// the request through — the cost of being wrong is a few
				// unthrottled requests. An auth check that can't decide must
				// not, because the cost of being wrong is an accepted
				// logged-out token.
				slog.Error("revocation check failed", "error", err, "path", r.URL.Path)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
				return
			}
			if revoked {
				// Same 401 body as a malformed token: the client learns the
				// token is unusable, not that it was specifically revoked.
				slog.Warn("rejected revoked token", "sub", claims["sub"], "path", r.URL.Path)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
				return
			}

			ctx := contextWithClaims(r.Context(), claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// validUserRole reports whether role is one of the roles the user form offers.
// Anything else (including empty) is rejected server-side even though the
// <select> only ever submits one of these values, since form submissions
// aren't trustworthy.
func validUserRole(role string) bool { return slices.Contains(staffRoles, role) }

// validUserRoleFilter reports whether role is a usable role *filter* value:
// either empty, meaning "all roles", or a role a user may actually hold. It
// mirrors validTripStatus and is shared by the users JSON endpoint and the
// users admin page.
func validUserRoleFilter(role string) bool { return role == "" || validUserRole(role) }

// handleLogout revokes the caller's own token and deletes their refresh
// tokens, ending the session server-side rather than relying on the client to
// discard it. It must be wrapped in requireAuth, which puts the validated
// claims on the context.
//
// Every user may log themselves out, so this is authenticated but not
// admin-gated. Because the admin UI's vp_session cookie carries the same JWT,
// logging out through the API also ends that browser session.
//
// Refresh tokens are not per-device, so logging out on one device invalidates
// refresh on all of them. That is the safe direction — the alternative is a
// logout the client can undo by refreshing — and per-device sessions would
// need a device identifier on refresh_tokens, which is a follow-up.
func handleLogout(revoker TokenRevoker, refreshTokens RefreshTokenDeleter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := r.Context().Value(claimsKey).(jwt.MapClaims)
		if !ok {
			slog.Warn("logout: claims missing from context")
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}

		// sub is a string, not a number (JSON number precision, see
		// generateJWT), so parse it rather than asserting a float64.
		sub, err := claims.GetSubject()
		if err != nil {
			slog.Warn("logout: unreadable sub claim", "error", err)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}
		userID, err := strconv.ParseInt(sub, 10, 64)
		if err != nil {
			slog.Warn("logout: sub claim is not a user ID", "sub", sub, "error", err)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}

		// Refresh tokens go first. If this succeeds and the revocation below
		// fails, the caller keeps a working access token until it expires but
		// cannot mint another; the reverse order would leave a revoked access
		// token that a surviving refresh token trades straight back in for a
		// new one, which is a logout that did not log anyone out.
		if err := refreshTokens.DeleteRefreshTokensForUser(r.Context(), userID); err != nil {
			slog.Error("logout: failed to delete refresh tokens", "sub", sub, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		jti, _ := claims["jti"].(string)
		if jti == "" {
			// A pre-revocation token (see checkRevoked) has nothing to record.
			// Report the same 204 so old and new clients see one contract; the
			// warning marks a session that outlives its logout. The refresh
			// tokens above are gone either way.
			slog.Warn("logout: token has no jti, nothing to revoke", "sub", sub)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// parseSessionToken requires exp, so a validated token always has one.
		expiresAt, err := claims.GetExpirationTime()
		if err != nil || expiresAt == nil {
			slog.Warn("logout: unreadable exp claim", "sub", sub, "error", err)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}

		if err := revoker.RevokeToken(r.Context(), jti, userID, expiresAt.Time); err != nil {
			slog.Error("logout: failed to revoke token", "sub", sub, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal server error"})
			return
		}

		slog.Info("token revoked", "sub", sub)
		w.WriteHeader(http.StatusNoContent)
	}
}
