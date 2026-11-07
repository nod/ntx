// Package adminui renders the admin and platform pages. Their templates and
// assets are embedded in the binary rather than coming from a site's theme,
// so they work on the platform host (which has no theme) and look the same
// on every site.
package adminui

import (
	"bytes"
	"embed"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"
)

//go:embed templates/*.html assets/*
var files embed.FS

var pageNames = []string{
	"login", "posts", "editor", "settings", "platform_sites", "platform_site_form", "platform_site_delete",
	"message", "signed_in", "invite", "members", "platform_users", "platform_user",
}

// pages holds one template set per page: the shared layout plus that page.
var pages = func() map[string]*template.Template {
	funcs := template.FuncMap{"join": strings.Join}
	m := make(map[string]*template.Template, len(pageNames))
	for _, p := range pageNames {
		m[p] = template.Must(template.New("layout.html").Funcs(funcs).ParseFS(files, "templates/layout.html", "templates/"+p+".html"))
	}
	return m
}()

// Render writes the admin page named page with status 200. data may be nil.
func Render(w http.ResponseWriter, page string, data map[string]any) {
	RenderStatus(w, http.StatusOK, page, data)
}

// RenderStatus writes the admin page named page with the given status.
func RenderStatus(w http.ResponseWriter, status int, page string, data map[string]any) {
	t, ok := pages[page]
	if !ok {
		log.Printf("adminui: unknown page %q", page)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		log.Printf("adminui: rendering %s: %v", page, err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(buf.Bytes())
}

// Assets serves admin.css and admin.js; mount it under /admin/assets/.
func Assets() http.Handler {
	sub, err := fs.Sub(files, "assets")
	if err != nil {
		panic(err)
	}
	return http.FileServerFS(sub)
}
