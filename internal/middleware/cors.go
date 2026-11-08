package middleware

import (
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// devOrigins are always allowed so local development keeps working.
var devOrigins = map[string]bool{
	"http://localhost:6500": true,
	"http://localhost:6501": true,
	"http://localhost:6502": true,
	"http://localhost:3000": true,
	"http://localhost:3001": true,
	"http://127.0.0.1:6500": true,
	"http://127.0.0.1:6501": true,
	"http://127.0.0.1:6502": true,
}

// ParseAllowedDomains splits a comma-separated ALLOWED_ORIGIN_DOMAIN value
// (e.g. "akademx.uz,farzandim-edu.uz") into normalized, non-empty domains.
// Call it once at startup, not per request.
func ParseAllowedDomains(raw string) []string {
	var domains []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		d := strings.ToLower(strings.TrimSpace(part))
		d = strings.TrimPrefix(d, "*.")
		d = strings.TrimPrefix(d, ".")
		if d != "" && !seen[d] {
			seen[d] = true
			domains = append(domains, d)
		}
	}
	return domains
}

// originAllowed reports whether origin is an https origin whose host is one of
// the given domains or a subdomain of one. The host is compared on a label
// boundary, so "https://evilakademx.uz" does NOT match "akademx.uz".
func originAllowed(origin string, domains []string) bool {
	if origin == "" || len(domains) == 0 {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return false
	}
	for _, d := range domains {
		if d == "" {
			continue
		}
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// CORS returns the CORS middleware. domains must come from ParseAllowedDomains.
func CORS(domains []string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")

		// The response depends on the Origin header; keep caches from mixing it up.
		c.Writer.Header().Add("Vary", "Origin")

		if devOrigins[origin] || originAllowed(origin, domains) {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-School-ID")
			c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")
		}

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	}
}
