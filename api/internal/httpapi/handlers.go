package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mandamas/api/internal/auth"
	"github.com/mandamas/api/internal/store"
	"github.com/mandamas/api/internal/validate"
)

type handlers struct{ store store.Store }

// decode reads a JSON body strictly: unknown fields and trailing data are errors.
func decode(c *gin.Context, v any) bool {
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(v)
	if err == nil && dec.More() {
		err = errors.New("unexpected data after JSON body")
	}
	if err == nil {
		return true
	}
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		fail(c, http.StatusRequestEntityTooLarge, "request is too large")
	case errors.Is(err, io.EOF):
		fail(c, http.StatusBadRequest, "empty body")
	default:
		fail(c, http.StatusBadRequest, "invalid JSON: "+err.Error())
	}
	return false
}

func fail(c *gin.Context, status int, msg string) { c.AbortWithStatusJSON(status, gin.H{"error": msg}) }

// storeErr maps store errors to HTTP; unexpected ones are logged and hidden.
func storeErr(c *gin.Context, err error, what string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(c, http.StatusNotFound, what+" not found")
	case errors.Is(err, store.ErrExists):
		fail(c, http.StatusConflict, what+" already exists")
	case errors.Is(err, store.ErrConflict):
		fail(c, http.StatusConflict, what+" changed, reload and try again")
	default:
		slog.Error("store error", "what", what, "err", err)
		fail(c, http.StatusInternalServerError, "something went wrong")
	}
}

func (h *handlers) me(c *gin.Context) { c.JSON(http.StatusOK, auth.FromContext(c)) }

// ---------- projects ----------

type projectBody struct {
	Key          string   `json:"key"`
	Label        string   `json:"label"`
	Description  string   `json:"description"`
	Color        string   `json:"color"`
	Logo         string   `json:"logo"`
	Categories   []string `json:"categories"`
	RoutineURL   string   `json:"routineUrl"`
	RoutineToken string   `json:"routineToken"`
}

// fields validates the public part and returns it cleaned.
func (b projectBody) fields() (store.ProjectFields, error) {
	f := store.ProjectFields{
		Label: strings.TrimSpace(b.Label), Description: strings.TrimSpace(b.Description),
		Color: b.Color, Logo: b.Logo,
	}
	if err := validate.Text("label", f.Label, 1, validate.MaxLabel); err != nil {
		return f, err
	}
	if err := validate.Text("description", f.Description, 0, validate.MaxDescription); err != nil {
		return f, err
	}
	if err := validate.Color(f.Color); err != nil {
		return f, err
	}
	if err := validate.Logo(f.Logo); err != nil {
		return f, err
	}
	cats, err := validate.Categories(b.Categories)
	f.Categories = cats
	return f, err
}

// secrets returns nil when both are empty (keep current), or validated secrets.
func (b projectBody) secrets() (*store.Secrets, error) {
	u, t := strings.TrimSpace(b.RoutineURL), strings.TrimSpace(b.RoutineToken)
	if u == "" && t == "" {
		return nil, nil
	}
	if err := validate.RoutineURL(u); err != nil {
		return nil, err
	}
	if err := validate.RoutineToken(t); err != nil {
		return nil, err
	}
	return &store.Secrets{RoutineURL: u, RoutineToken: t}, nil
}

func (h *handlers) listProjects(c *gin.Context) {
	ps, err := h.store.ListProjects(c.Request.Context())
	if err != nil {
		storeErr(c, err, "projects")
		return
	}
	c.JSON(http.StatusOK, gin.H{"projects": ps})
}

func (h *handlers) getProject(c *gin.Context) {
	p, err := h.store.GetProject(c.Request.Context(), c.Param("key"))
	if err != nil {
		storeErr(c, err, "project")
		return
	}
	c.JSON(http.StatusOK, p)
}

func (h *handlers) createProject(c *gin.Context) {
	var b projectBody
	if !decode(c, &b) {
		return
	}
	if err := validate.Key(b.Key); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	f, err := b.fields()
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	sec, err := b.secrets()
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if sec == nil {
		fail(c, http.StatusBadRequest, "routine URL and token are required")
		return
	}
	p, err := h.store.CreateProject(c.Request.Context(), b.Key, f, auth.FromContext(c).Email, *sec)
	if err != nil {
		storeErr(c, err, "project "+b.Key)
		return
	}
	c.JSON(http.StatusCreated, p)
}

func (h *handlers) updateProject(c *gin.Context) {
	var b projectBody
	if !decode(c, &b) {
		return
	}
	if b.Key != "" && b.Key != c.Param("key") {
		fail(c, http.StatusBadRequest, "the key can't change")
		return
	}
	f, err := b.fields()
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	sec, err := b.secrets()
	if err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	p, err := h.store.UpdateProject(c.Request.Context(), c.Param("key"), f, sec)
	if err != nil {
		storeErr(c, err, "project")
		return
	}
	c.JSON(http.StatusOK, p)
}

func (h *handlers) archiveProject(c *gin.Context) {
	if err := h.store.ArchiveProject(c.Request.Context(), c.Param("key")); err != nil {
		storeErr(c, err, "project")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// ---------- tasks ----------

func (h *handlers) listTasks(c *gin.Context) {
	ts, err := h.store.ListTasks(c.Request.Context(), c.Param("key"), 100)
	if err != nil {
		storeErr(c, err, "tasks")
		return
	}
	c.JSON(http.StatusOK, gin.H{"tasks": ts})
}

type taskBody struct {
	Project     string `json:"project"`
	Category    string `json:"category"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func (h *handlers) createTask(c *gin.Context) {
	var b taskBody
	if !decode(c, &b) {
		return
	}
	b.Title, b.Description = strings.TrimSpace(b.Title), strings.TrimSpace(b.Description)
	if err := validate.Key(b.Project); err != nil {
		fail(c, http.StatusBadRequest, "project: "+err.Error())
		return
	}
	if err := validate.Text("title", b.Title, 1, validate.MaxTitle); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := validate.Text("description", b.Description, 0, validate.MaxTaskText); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	p, err := h.store.GetProject(c.Request.Context(), b.Project)
	if err != nil {
		storeErr(c, err, "project")
		return
	}
	if p.Archived {
		fail(c, http.StatusConflict, "this project is archived")
		return
	}
	cat := strings.ToLower(strings.TrimSpace(b.Category))
	allowed := false
	for _, x := range p.Categories {
		allowed = allowed || x == cat
	}
	if !allowed {
		fail(c, http.StatusBadRequest, "category isn't enabled for this project")
		return
	}
	t, err := h.store.CreateTask(c.Request.Context(), store.Task{
		Project: b.Project, Category: cat, Title: b.Title, Description: b.Description, CreatedBy: auth.FromContext(c).Email,
	})
	if err != nil {
		storeErr(c, err, "task")
		return
	}
	c.JSON(http.StatusCreated, t)
}

func (h *handlers) answerTask(c *gin.Context) {
	id := c.Param("id")
	if err := validate.TaskID(id); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	var b struct {
		Answer string `json:"answer"`
	}
	if !decode(c, &b) {
		return
	}
	b.Answer = strings.TrimSpace(b.Answer)
	if err := validate.Text("answer", b.Answer, 1, validate.MaxAnswer); err != nil {
		fail(c, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.store.AnswerTask(c.Request.Context(), id, b.Answer); err != nil {
		if errors.Is(err, store.ErrConflict) {
			fail(c, http.StatusConflict, "this question was already answered")
			return
		}
		storeErr(c, err, "task")
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
