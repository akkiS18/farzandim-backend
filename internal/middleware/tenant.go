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
			subdomain := extractSubdomain(c)
			resolvedID, err := db.FindSchoolIDBySubdomain(subdomain)
			if err == nil && resolvedID != "" {
				schoolID = resolvedID
			} else {
				log.Printf("[TENANT RESOLUTION WARNING] Subdomain '%s' resolution error: %v", subdomain, err)
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

func extractSubdomain(c *gin.Context) string {
	rawURL := c.GetHeader("Origin")
	if rawURL == "" {
		rawURL = c.GetHeader("Referer")
	}

	var host string
	if rawURL != "" {
		if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
			host = u.Host
		}
	}

	if host == "" {
		host = c.GetHeader("X-Forwarded-Host")
	}
	if host == "" {
		host = c.Request.Host
	}

	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}

	if host == "localhost" || host == "127.0.0.1" || strings.HasPrefix(host, "::1") {
		return "localhost"
	}

	parts := strings.Split(host, ".")
	if len(parts) >= 3 {
		// e.g. test_school.akademx.uz -> test_school
		return parts[0]
	}

	return host
}
