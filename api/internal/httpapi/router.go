// Package httpapi wires the Gin router: /api (auth + JSON handlers) and the static app.
package httpapi

import (
	"github.com/gin-gonic/gin"

	"github.com/mandamas/api/internal/allowlist"
	"github.com/mandamas/api/internal/auth"
	"github.com/mandamas/api/internal/store"
)

type Options struct {
	Verifier       auth.Verifier
	Allowlist      allowlist.Checker
	Store          store.Store
	AppDir         string // static app folder; empty disables static serving
	TrustedProxies []string
	MaxBody        int64   // bytes; default 512 KB
	RatePerSecond  float64 // per user; default 1
	RateBurst      int     // default 30
}

func NewRouter(o Options) *gin.Engine {
	if o.MaxBody == 0 {
		o.MaxBody = 512 << 10
	}
	if o.RatePerSecond == 0 {
		o.RatePerSecond, o.RateBurst = 1, 30
	}
	r := gin.New()
	_ = r.SetTrustedProxies(o.TrustedProxies) // nil = trust none
	r.Use(gin.Recovery(), requestLog(), securityHeaders())

	r.GET("/healthz", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	h := &handlers{store: o.Store}
	api := r.Group("/api", noStore(), limitBody(o.MaxBody), auth.Middleware(o.Verifier, o.Allowlist), rateLimit(o.RatePerSecond, o.RateBurst), jsonWrites())
	api.GET("/me", h.me)
	api.GET("/projects", h.listProjects)
	api.POST("/projects", h.createProject)
	api.GET("/projects/:key", validKey(), h.getProject)
	api.PATCH("/projects/:key", validKey(), h.updateProject)
	api.POST("/projects/:key/archive", validKey(), h.archiveProject)
	api.GET("/projects/:key/tasks", validKey(), h.listTasks)
	api.POST("/tasks", h.createTask)
	api.POST("/tasks/:id/answer", h.answerTask)

	if o.AppDir != "" {
		r.NoRoute(staticApp(o.AppDir))
	} else {
		r.NoRoute(func(c *gin.Context) { c.JSON(404, gin.H{"error": "not found"}) })
	}
	return r
}
