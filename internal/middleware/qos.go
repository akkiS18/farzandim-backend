package middleware

import (
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type SchoolQoSLimiter struct {
	parentSem map[string]chan struct{}
	globalSem chan struct{}
	mu        sync.Mutex
	parentMax int
}

var QoSLimiter *SchoolQoSLimiter

func InitQoS() {
	parentMax := 15
	if envMax := os.Getenv("MAX_PARENT_CONCURRENCY"); envMax != "" {
		if val, err := strconv.Atoi(envMax); err == nil && val > 0 {
			parentMax = val
		}
	}

	globalMax := parentMax * 3
	if globalMax < 30 {
		globalMax = 30
	}

	QoSLimiter = &SchoolQoSLimiter{
		parentSem: make(map[string]chan struct{}),
		globalSem: make(chan struct{}, globalMax),
		parentMax: parentMax,
	}
}

func (q *SchoolQoSLimiter) getTenantParentSem(schoolID string) chan struct{} {
	q.mu.Lock()
	defer q.mu.Unlock()

	sem, exists := q.parentSem[schoolID]
	if !exists {
		sem = make(chan struct{}, q.parentMax)
		q.parentSem[schoolID] = sem
	}
	return sem
}

// QoSMiddleware prioritizes TEACHER and ADMIN requests into VIP lane,
// while protecting database capacity with bounded concurrency for PARENT and STUDENT traffic.
func QoSMiddleware() gin.HandlerFunc {
	if QoSLimiter == nil {
		InitQoS()
	}

	return func(c *gin.Context) {
		roleVal, exists := c.Get("role")
		role := ""
		if exists {
			if r, ok := roleVal.(string); ok {
				role = r
			}
		}

		// 1. VIP LANE: Teachers, Main Teachers, Admins, Directors bypass all throttles
		if role == "ADMIN" || role == "TEACHER" || role == "MAIN_TEACHER" || role == "DIRECTOR" {
			c.Next()
			return
		}

		// 2. PROTECTED LANE: Parents, Students, and Guests
		schoolIDVal, _ := c.Get("schoolID")
		schoolID, _ := schoolIDVal.(string)
		if schoolID == "" {
			schoolID = "default"
		}

		sem := QoSLimiter.getTenantParentSem(schoolID)

		// Acquire slot with a timeout of 1.5 seconds
		select {
		case sem <- struct{}{}:
			defer func() {
				<-sem
			}()
			c.Next()
		case <-time.After(1500 * time.Millisecond):
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error": "Tizimda yuklama yuqori bo'lganligi sababli biroz kuting va qayta urinib ko'ring (QoS Protected).",
				"retry_after_seconds": 3,
			})
			c.Abort()
			return
		}
	}
}
