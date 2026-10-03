package handlers

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
)

// Generate renders every configured site's static output into its own
// <outDir>/<site.key>/ subdirectory.
func (h *Handler) Generate(outDir string) error {
	for _, sh := range h.sites {
		siteOut := filepath.Join(outDir, sh.site.Key)
		if err := h.generateSite(sh, siteOut); err != nil {
			return fmt.Errorf("generating site %q: %w", sh.site.Key, err)
		}
	}
	return nil
}

func (h *Handler) generateSite(sh *siteHandler, outDir string) error {
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	if err := h.genPage(sh, outDir, "/", "home.html", nil); err != nil {
		return err
	}

	posts, err := sh.posts.List(true)
	if err != nil {
		return fmt.Errorf("listing posts: %w", err)
	}
	allTags, err := sh.posts.AllTags()
	if err != nil {
		return fmt.Errorf("listing tags: %w", err)
	}
	if err := h.genPage(sh, outDir, "/blog", "blog/index.html", map[string]interface{}{
		"Posts":     posts,
		"AllTags":   allTags,
		"ActiveTag": "",
	}); err != nil {
		return err
	}

	for _, p := range posts {
		post := p
		rendered, err := renderMarkdown(post.Body)
		if err != nil {
			return fmt.Errorf("rendering markdown for %s: %w", post.Slug, err)
		}
		if err := h.genPage(sh, outDir, "/blog/"+post.Slug, "blog/post.html", map[string]interface{}{
			"Post":     &post,
			"Rendered": rendered,
		}); err != nil {
			return err
		}
	}

	for _, sec := range sh.site.Sections {
		for _, pg := range sec.Pages {
			tmplName := "sections/" + sec.Slug + "/" + pg.Slug + "/index.html"
			if !sh.templates.Has(tmplName) {
				log.Printf("warning: template %s not found, skipping /%s/%s", tmplName, sec.Slug, pg.Slug)
				continue
			}
			data := make(map[string]interface{})
			for k, v := range sec.Vars {
				data[k] = v
			}
			if sec.Title != "" {
				data["SectionTitle"] = sec.Title
			}
			for k, v := range pg.Vars {
				data[k] = v
			}
			if pg.Title != "" {
				data["Title"] = pg.Title
			}
			if err := h.genPage(sh, outDir, "/"+sec.Slug+"/"+pg.Slug, tmplName, data); err != nil {
				return err
			}
		}
	}

	if err := copyDir(sh.site.StaticPath, filepath.Join(outDir, "static")); err != nil {
		return fmt.Errorf("copying static files: %w", err)
	}
	log.Printf("copied static/ → %s/static/", outDir)

	return nil
}

func (h *Handler) genPage(sh *siteHandler, outDir, urlPath, tmplName string, data map[string]interface{}) error {
	content, err := h.renderToBytes(sh, tmplName, data)
	if err != nil {
		return fmt.Errorf("rendering %s: %w", urlPath, err)
	}

	var filePath string
	if urlPath == "/" {
		filePath = filepath.Join(outDir, "index.html")
	} else {
		filePath = filepath.Join(outDir, urlPath, "index.html")
	}

	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return fmt.Errorf("mkdir for %s: %w", filePath, err)
	}
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", filePath, err)
	}
	log.Printf("generated %s%s", sh.site.Key, urlPath)
	return nil
}

func (h *Handler) renderToBytes(sh *siteHandler, name string, data map[string]interface{}) ([]byte, error) {
	t, err := sh.templates.Get(name)
	if err != nil {
		return nil, err
	}
	merged := sh.siteData()
	for k, v := range data {
		merged[k] = v
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, merged); err != nil {
		return nil, fmt.Errorf("executing %s: %w", name, err)
	}
	return buf.Bytes(), nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}
