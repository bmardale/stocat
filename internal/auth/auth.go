package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bmardale/stocat/internal/apierr"
	"github.com/bmardale/stocat/internal/db"
	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	CookieName      = "stocat_session"
	sessionLifetime = 30 * 24 * time.Hour
	securityScheme  = "session"

	maxUserAgentBytes = 512
)

type Config struct {
	SecureCookies bool
	Logger        *slog.Logger
}

type Service struct {
	pool          *pgxpool.Pool
	queries       *db.Queries
	secureCookies bool
	log           *slog.Logger
}

type User struct {
	ID        int64     `json:"-"`
	PublicID  string    `json:"public_id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
	IsAdmin   bool      `json:"is_admin"`
}

type userKey struct{}
type metadataKey struct{}

type requestMetadata struct {
	userAgent string
	ip        *netip.Addr
	token     string
}

func New(pool *pgxpool.Pool, cfg Config) *Service {
	log := cfg.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Service{pool: pool, queries: db.New(pool), secureCookies: cfg.SecureCookies, log: log}
}

func UserFromContext(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(userKey{}).(User)
	return user, ok
}

// Protected applies session authentication and documents it for every route in the group.
// Add authorization middleware to this group or its child groups.
func (s *Service) Protected(api huma.API, prefixes ...string) *huma.Group {
	if api.OpenAPI().Components.SecuritySchemes == nil {
		api.OpenAPI().Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	api.OpenAPI().Components.SecuritySchemes[securityScheme] = &huma.SecurityScheme{
		Type: "apiKey", In: "cookie", Name: CookieName,
	}
	group := huma.NewGroup(api, prefixes...)
	group.UseSimpleModifier(func(op *huma.Operation) {
		op.Security = []map[string][]string{{securityScheme: {}}}
		for _, status := range []int{http.StatusUnauthorized, http.StatusInternalServerError} {
			op.Responses[strconv.Itoa(status)] = &huma.Response{
				Description: http.StatusText(status),
				Content: map[string]*huma.MediaType{apierr.ContentType: {
					Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeFor[apierr.Problem](), true, "Problem"),
				}},
			}
		}
	})
	group.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
		ctx.SetHeader("Cache-Control", "no-store")
		cookie, err := huma.ReadCookie(ctx, CookieName)
		var hash []byte
		if err == nil {
			hash = tokenHash(cookie.Value)
		}
		if hash == nil {
			s.writeAuthError(api, ctx, http.StatusUnauthorized, "Sign in to continue.")
			return
		}
		user, err := s.queries.GetSessionUser(ctx.Context(), hash)
		if errors.Is(err, pgx.ErrNoRows) {
			s.writeAuthError(api, ctx, http.StatusUnauthorized, "Sign in to continue.")
			return
		}
		if err != nil {
			s.log.ErrorContext(ctx.Context(), "load session", "error", err)
			s.writeAuthError(api, ctx, http.StatusInternalServerError, "Authentication is unavailable.")
			return
		}
		next(huma.WithValue(ctx, userKey{}, publicUser(user)))
	})
	return group
}

func (s *Service) writeAuthError(api huma.API, ctx huma.Context, status int, message string) {
	if err := huma.WriteErr(api, ctx, status, message); err != nil {
		s.log.ErrorContext(ctx.Context(), "write authentication error", "error", err)
	}
}

func publicUser(user db.User) User {
	return User{ID: user.ID, PublicID: user.PublicID, Name: user.Name, Email: user.Email,
		CreatedAt: user.CreatedAt.Time, IsAdmin: user.IsAdmin}
}

func captureMetadata(ctx huma.Context, next func(huma.Context)) {
	meta := requestMetadata{userAgent: truncateUserAgent(ctx.Header("User-Agent"))}
	host, _, err := net.SplitHostPort(ctx.RemoteAddr())
	if err != nil {
		host = ctx.RemoteAddr()
	}
	if addr, err := netip.ParseAddr(host); err == nil {
		addr = addr.Unmap()
		meta.ip = &addr
	}
	if cookie, err := huma.ReadCookie(ctx, CookieName); err == nil {
		meta.token = cookie.Value
	}
	ctx.SetHeader("Cache-Control", "no-store")
	next(huma.WithValue(ctx, metadataKey{}, meta))
}

// PostgreSQL rejects invalid UTF-8 in TEXT columns. HTTP headers can contain it.
func truncateUserAgent(userAgent string) string {
	userAgent = strings.ToValidUTF8(userAgent, "\uFFFD")
	if len(userAgent) <= maxUserAgentBytes {
		return userAgent
	}
	n := maxUserAgentBytes
	for !utf8.RuneStart(userAgent[n]) {
		n--
	}
	return userAgent[:n]
}

func tokenHash(token string) []byte {
	if len(token) != 64 {
		return nil
	}
	raw, err := hex.DecodeString(token)
	if err != nil {
		return nil
	}
	hash := sha256.Sum256(raw)
	return hash[:]
}

func (s *Service) createSession(ctx context.Context, queries *db.Queries, userID int64) (http.Cookie, error) {
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	token := hex.EncodeToString(raw)
	expires := time.Now().Add(sessionLifetime).Truncate(time.Second)
	meta, _ := ctx.Value(metadataKey{}).(requestMetadata)
	err := queries.CreateSession(ctx, db.CreateSessionParams{
		TokenHash: tokenHash(token), UserID: userID, UserAgent: meta.userAgent, IpAddress: meta.ip,
		ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true},
	})
	if err != nil {
		return http.Cookie{}, err
	}
	if hash := tokenHash(meta.token); hash != nil {
		if err := queries.DeleteSession(ctx, hash); err != nil {
			return http.Cookie{}, err
		}
	}
	cookie := s.cookie(token)
	cookie.Expires = expires
	cookie.MaxAge = int(sessionLifetime.Seconds())
	return cookie, nil
}

func (s *Service) cookie(token string) http.Cookie {
	return http.Cookie{Name: CookieName, Value: token, Path: "/", HttpOnly: true,
		Secure: s.secureCookies, SameSite: http.SameSiteLaxMode}
}
