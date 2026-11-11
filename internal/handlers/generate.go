package handlers

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/nod/ntx/internal/models"
	"github.com/nod/ntx/internal/themes"
)

// Generate renders every site's static output into <outDir>/<site.key>/,
// in key order, stopping at the first site that fails.
func (h *Handler) Generate(outDir string) error {
	for _, site := range h.d.Registry.All() {
		site := site
		if err := h.generateSite(&site, filepath.Join(outDir, site.Key)); err != nil {
			return fmt.Errorf("generating site %q: %w", site.Key, err)
		}
	}
	return nil
}

func (h *Handler) generateSite(site *models.Site, outDir string) error {
	themeDir, err := themes.Dir(h.d.ThemesPath, site.Theme)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	if err := h.genPage(site, outDir, "/", "home.html", nil); err != nil {
		return err
	}

	ps := h.posts(site)
	posts, err := ps.List(true)
	if err != nil {
		return fmt.Errorf("listing posts: %w", err)
	}
	allTags, err := ps.AllTags()
	if err != nil {
		return fmt.Errorf("listing tags: %w", err)
	}
	if err := h.genPage(site, outDir, "/blog", "blog/index.html", map[string]interface{}{
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
		if err := h.genPage(site, outDir, "/blog/"+post.Slug, "blog/post.html", map[string]interface{}{
			"Post":     &post,
			"Rendered": rendered,
		}); err != nil {
			return err
		}
	}

	tc, err := h.templates(site)
	if err != nil {
		return err
	}
	for i := range site.Sections {
		sec := &site.Sections[i]
		for j := range sec.Pages {
			pg := &sec.Pages[j]
			tmplName := "sections/" + sec.Slug + "/" + pg.Slug + "/index.html"
			has, err := tc.Has(tmplName)
			if err != nil {
				return fmt.Errorf("theme %q: %w", site.Theme, err)
			}
			if !has {
				log.Printf("warning: template %s not found, skipping /%s/%s", tmplName, sec.Slug, pg.Slug)
				continue
			}
			if err := h.genPage(site, outDir, "/"+sec.Slug+"/"+pg.Slug, tmplName, sectionData(sec, pg)); err != nil {
				return err
			}
		}
	}

	static := filepath.Join(themeDir, "static")
	if _, err := os.Stat(static); err == nil {
		if err := copyDir(static, filepath.Join(outDir, "static")); err != nil {
			return fmt.Errorf("copying static files: %w", err)
		}
		log.Printf("copied static/ → %s/static/", outDir)
	}
	return nil
}

func (h *Handler) genPage(site *models.Site, outDir, urlPath, tmplName string, data map[string]interface{}) error {
	content, err := h.renderToBytes(site, tmplName, data)
	if err != nil {
		return fmt.Errorf("rendering %s: %w", urlPath, err)
	}
	filePath := filepath.Join(outDir, "index.html")
	if urlPath != "/" {
		filePath = filepath.Join(outDir, urlPath, "index.html")
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		return fmt.Errorf("mkdir for %s: %w", filePath, err)
	}
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		return fmt.Errorf("writing %s: %w", filePath, err)
	}
	log.Printf("generated %s%s", site.Key, urlPath)
	return nil
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
