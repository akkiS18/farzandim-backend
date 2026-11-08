package middleware

import (
	"bytes"
	"encoding/json"
	"io"
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

		// 2. Fall back to resolving school dynamically by request subdomain (skip generic non-school subdomains like games/localhost on login)
		subdomain := extractSubdomain(c)
		isLoginPath := strings.HasSuffix(c.Request.URL.Path, "/login")
		isGenericSubdomain := subdomain == "localhost" || subdomain == "games" || subdomain == "api" || subdomain == "www"

		if schoolID == "" && !(isLoginPath && isGenericSubdomain) {
			resolvedID, err := db.FindSchoolIDBySubdomain(subdomain)
			if err == nil && resolvedID != "" {
				schoolID = resolvedID
			} else {
				log.Printf("[TENANT RESOLUTION WARNING] Subdomain '%s' resolution error: %v", subdomain, err)
			}
		}

		// 3. If still empty and this is a login request, try auto-resolving school by user identifier (phone or passport)
		if schoolID == "" && isLoginPath {
			bodyBytes, err := io.ReadAll(c.Request.Body)
			if err == nil && len(bodyBytes) > 0 {
				c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // restore body for downstream handlers
				var loginReq struct {
					Phone      string `json:"phone"`
					DocumentNo string `json:"document_no"`
				}
				if json.Unmarshal(bodyBytes, &loginReq) == nil {
					identifier := loginReq.DocumentNo
					if identifier == "" {
						identifier = loginReq.Phone
					}
					if identifier != "" {
						if autoSchoolID, err := db.FindSchoolIDByUserIdentifier(identifier); err == nil && autoSchoolID != "" {
							schoolID = autoSchoolID
							log.Printf("[TENANT AUTO-RESOLVE] User '%s' auto-routed to school ID '%s'", identifier, autoSchoolID)
						}
					}
				}
			}
			// If auto-resolve didn't match a unique school on localhost, fall back to subdomain resolution
			if schoolID == "" && isGenericSubdomain {
				if resolvedID, err := db.FindSchoolIDBySubdomain(subdomain); err == nil && resolvedID != "" {
					schoolID = resolvedID
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
