package httpapi

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"

	"github.com/mandamas/api/internal/auth"
	"github.com/mandamas/api/internal/validate"
)

// requestLog logs method, path, status, latency and user. Never headers, query strings, tokens or bodies.
func requestLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		if !strings.HasPrefix(c.Request.URL.Path, "/api/") {
			return
		}
		slog.Info("request", "method", c.Request.Method, "path", c.Request.URL.Path, "status", c.Writer.Status(),
			"ms", time.Since(start).Milliseconds(), "user", auth.FromContext(c).Email)
	}
}

func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("X-Frame-Options", "DENY")
		c.Next()
	}
}

func noStore() gin.HandlerFunc {
	return func(c *gin.Context) { c.Header("Cache-Control", "no-store"); c.Next() }
}

func limitBody(n int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, n)
		c.Next()
	}
}

// jsonWrites requires a JSON body on POST/PATCH/PUT.
func jsonWrites() gin.HandlerFunc {
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodPost, http.MethodPatch, http.MethodPut:
			if !strings.HasPrefix(c.ContentType(), "application/json") {
				c.AbortWithStatusJSON(http.StatusUnsupportedMediaType, gin.H{"error": "send JSON (Content-Type: application/json)"})
				return
			}
		}
		c.Next()
	}
}

func validKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := validate.Key(c.Param("key")); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		c.Next()
	}
}

// rateLimit is a per-user token bucket (few users, so the map stays tiny).
func rateLimit(perSecond float64, burst int) gin.HandlerFunc {
	var mu sync.Mutex
	buckets := map[string]*rate.Limiter{}
	return func(c *gin.Context) {
		key := auth.FromContext(c).UID
		mu.Lock()
		l, ok := buckets[key]
		if !ok {
			l = rate.NewLimiter(rate.Limit(perSecond), burst)
			buckets[key] = l
		}
		mu.Unlock()
		if !l.Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too many requests, slow down"})
			return
		}
		c.Next()
	}
}
