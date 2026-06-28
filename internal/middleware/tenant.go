package middleware

import (
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/farzandim/backend/internal/db"
	"github.com/gin-gonic/gin"
)

func TenantMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 1. Attempt to resolve School ID from header, falling back to JWT context
		schoolID := c.GetHeader("X-School-ID")
		if schoolID == "" {
			if ctxSchoolID, exists := c.Get("schoolID"); exists {
				if strID, ok := ctxSchoolID.(string); ok && strID != "" {
					schoolID = strID
				}
			}
		}

		// 2. Fall back to resolving school dynamically by request subdomain
		if schoolID == "" {
			origin := c.GetHeader("Origin")
			referer := c.GetHeader("Referer")
			subdomain := extractSubdomain(origin, referer)
			if subdomain != "" {
				resolvedID, err := db.FindSchoolIDBySubdomain(subdomain)
				if err == nil {
					schoolID = resolvedID
				} else {
					log.Printf("[TENANT RESOLUTION WARNING] %v", err)
				}
			}
		}

		if schoolID == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Tenant routing failed: Could not determine school database from subdomain, headers, or token context"})
			c.Abort()
			return
		}

		// 3. Resolve database connection pool from the manager
		tenantDB, err := db.TenantConnManager.GetTenantDB(schoolID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{
				"error":   "Failed to route request to tenant database",
				"details": err.Error(),
			})
			c.Abort()
			return
		}

		// 4. Set the DB pool in the request context for handlers to consume
		c.Set("tenantDB", tenantDB)
		c.Set("currentSchoolID", schoolID)

		c.Next()
	}
}

func extractSubdomain(origin, referer string) string {
	rawURL := origin
	if rawURL == "" {
		rawURL = referer
	}
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := u.Host
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}
	
	if host == "localhost" || host == "127.0.0.1" || strings.HasPrefix(host, "::1") {
		return "localhost"
	}
	
	// Split by "." to find subdomain
	parts := strings.Split(host, ".")
	if len(parts) > 2 {
		// e.g. 10_maktab.farzandim.uz -> 10_maktab
		return parts[0]
	}
	
	return ""
}
