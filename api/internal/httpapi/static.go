package httpapi

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
)

// staticApp serves the PWA folder. Unknown paths fall back to index.html; /api paths never do.
func staticApp(dir string) gin.HandlerFunc {
	root, _ := filepath.Abs(dir)
	return func(c *gin.Context) {
		p := path.Clean("/" + c.Request.URL.Path)
		if strings.HasPrefix(p, "/api/") || p == "/api" {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
			c.Status(http.StatusMethodNotAllowed)
			return
		}
		file := filepath.Join(root, filepath.FromSlash(p))
		if st, err := os.Stat(file); err != nil || st.IsDir() {
			file, p = filepath.Join(root, "index.html"), "/index.html"
		}
		switch {
		case p == "/index.html" || p == "/sw.js" || strings.HasSuffix(p, ".webmanifest"):
			c.Header("Cache-Control", "no-cache")
		case strings.HasPrefix(p, "/icons/") || strings.HasPrefix(p, "/img/") || strings.HasPrefix(p, "/assets/"):
			c.Header("Cache-Control", "public, max-age=604800")
		default:
			c.Header("Cache-Control", "public, max-age=0, must-revalidate")
		}
		if strings.HasSuffix(p, ".webmanifest") {
			c.Header("Content-Type", "application/manifest+json")
		}
		http.ServeFile(c.Writer, c.Request, file)
	}
}
