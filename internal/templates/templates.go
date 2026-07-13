package templates

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

//go:embed all:views
var viewsFS embed.FS

// Engine wraps html/template with the project's base layout + helpers.
// Each render clones the master template set so we can pick a different
// {{define "body"}} per page (html/template only allows one body block per
// template-set at a time).
type Engine struct {
	master *template.Template
}

func New() (*Engine, error) {
	funcs := template.FuncMap{
		"safeHTML": func(s string) template.HTML { return template.HTML(s) },
		"fmtDate":  func(t time.Time) string { return t.Format("Jan 2, 2006") },
		"upper":    strings.ToUpper,
	}
	t := template.New("").Funcs(funcs)
	if err := walk(t, viewsFS, "views"); err != nil {
		return nil, err
	}
	return &Engine{master: t}, nil
}

func walk(t *template.Template, root fs.FS, dir string) error {
	entries, err := fs.ReadDir(root, dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		p := path.Join(dir, e.Name())
		if e.IsDir() {
			if err := walk(t, root, p); err != nil {
				return err
			}
			continue
		}
		if !strings.HasSuffix(e.Name(), ".html") {
			continue
		}
		b, err := fs.ReadFile(root, p)
		if err != nil {
			return err
		}
		name := strings.TrimPrefix(p, "views/")
		if _, err := t.New(name).Parse(string(b)); err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
	}
	return nil
}

// Render executes "base" with the supplied data. The page supplies its own
// `body` block by re-parsing the requested page file into a fresh clone
// and binding `body` to the page's `body` definition.
func (e *Engine) Render(w io.Writer, pageName string, data any) error {
	clone, err := e.master.Clone()
	if err != nil {
		return err
	}
	b, err := fs.ReadFile(viewsFS, "views/pages/"+pageName+".html")
	if err != nil {
		return fmt.Errorf("read page %s: %w", pageName, err)
	}
	if _, err := clone.Parse(string(b)); err != nil {
		return fmt.Errorf("parse page %s: %w", pageName, err)
	}
	return clone.ExecuteTemplate(w, "base", data)
}

// RenderPartial renders a single partial template (e.g. "partials/user_message.html")
// against a fresh clone. Useful for HTMX fragments.
func (e *Engine) RenderPartial(w io.Writer, partialPath string, data any) error {
	clone, err := e.master.Clone()
	if err != nil {
		return err
	}
	b, err := fs.ReadFile(viewsFS, "views/"+partialPath)
	if err != nil {
		return fmt.Errorf("read partial %s: %w", partialPath, err)
	}
	if _, err := clone.Parse(string(b)); err != nil {
		return fmt.Errorf("parse partial %s: %w", partialPath, err)
	}
	// The partial defines blocks like {{define "user_message"}}; execute the
	// one matching the file name (basename without .html).
	name := strings.TrimSuffix(path.Base(partialPath), ".html")
	return clone.ExecuteTemplate(w, name, data)
}

// RenderPage sets the content type and delegates to Render.
func (e *Engine) RenderPage(w http.ResponseWriter, pageName string, data any) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return e.Render(w, pageName, data)
}
