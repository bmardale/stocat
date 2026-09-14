package audit

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"unicode/utf8"
)

const maxUserAgentBytes = 512

type requestKey struct{}

type requestMetadata struct {
	ip        *netip.Addr
	userAgent string
}

// Middleware keeps the client address and user agent for events that the request records.
// It ignores forwarding headers, because no trusted proxy removes them.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		meta := requestMetadata{userAgent: truncateUserAgent(r.UserAgent())}
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if addr, err := netip.ParseAddr(host); err == nil {
			addr = addr.Unmap()
			meta.ip = &addr
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestKey{}, meta)))
	})
}

// A background job has no request, so its events have no client metadata.
func requestFrom(ctx context.Context) requestMetadata {
	meta, _ := ctx.Value(requestKey{}).(requestMetadata)
	return meta
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
