package middleware

import (
	"database/sql"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/farzandim/backend/internal/db"
	"github.com/gin-gonic/gin"
)

// Only central DB accounts with a current session version may use admin APIs.
// Old tokens without auth_version intentionally require a new login.
func RequireActiveSuperAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if c.GetHeader("Authorization") == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Authorization header required"})
			return
		}
		var version int
		err := db.CentralDB.QueryRowContext(c.Request.Context(),
			"SELECT auth_version FROM super_admins WHERE id = $1 AND is_deleted = false",
			c.GetString("userID")).Scan(&version)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "Authentication service unavailable"})
			return
		}
		if err != nil || version != c.GetInt("authVersion") || version < 1 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Super-admin session expired; sign in again"})
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}

// Bounds attempts per network peer without trusting spoofable forwarding headers.
// A reverse proxy shares one bucket; also enforce per-client limits at that proxy.
func SuperAdminLoginLimit() gin.HandlerFunc {
	type bucket struct {
		count int
		until time.Time
	}
	var mu sync.Mutex
	entries := make(map[string]bucket)
	return func(c *gin.Context) {
		host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil {
			host = c.Request.RemoteAddr
		}
		now := time.Now()
		mu.Lock()
		for key, entry := range entries {
			if !now.Before(entry.until) {
				delete(entries, key)
			}
		}
		entry, exists := entries[host]
		if !exists {
			if len(entries) >= 10000 {
				mu.Unlock()
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "Too many login attempts; try again later"})
				return
			}
			entry.until = now.Add(time.Minute)
		}
		entry.count++
		entries[host] = entry
		limited := entry.count > 10
		mu.Unlock()
		if limited {
			c.Header("Retry-After", "60")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "Too many login attempts; try again in a minute"})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		c.Header("Cache-Control", "no-store")
		c.Next()
	}
}
