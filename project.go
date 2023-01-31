package main

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SEP is a browser generated UUID v4 used to separate out commit line items.
const SEP = "6f6c1745-e902-474a-9e99-08d0084fb011"

// Helps keep track of file extensions git thinks of as binary.
var types = make(map[string]bool)

type project struct {
	base     string
	Name     string
	repo     string
	options  *options
	template *template.Template
}

func NewProject(base string, repo string, options *options) *project {
	funcMap := template.FuncMap{
		"diffstatbodyparser": diffstatbodyparser,
		"diffbodyparser":     diffbodyparser,
	}
	template := template.Must(template.New("page").Funcs(funcMap).Parse(tpl))

	return &project{
		base:     base,
		Name:     options.Name,
		repo:     repo,
		options:  options,
		template: template,
	}
}

// Creates base directories for holding objects, branches, and commits.
func (p *project) init() error {
	dirs := []string{"branch", "commit", "object"}

	for _, dir := range dirs {
		d := filepath.Join(p.base, dir)

		// Clear existing dirs when -f true.
		if p.options.Force && dir != "branch" {
			if err := os.RemoveAll(d); err != nil {
				return fmt.Errorf("unable to remove directory: %v", err)
			}
		}

		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("unable to create directory: %v", err)
		}
	}

	return nil
}

// Saves a local clone of `target` repo.
func (p *project) save() error {
	if _, err := os.Stat(p.repo); err != nil {
		return err
	}

	return exec.Command("git", "clone", p.options.Source, p.repo).Run()
}

func (p *project) updateBranches(branches []branch) {
	for _, b := range branches {
		// NOTE: Is this needed still if the repo is downloaded each time the script is run?
		ref := fmt.Sprintf("refs/heads/%s:refs/origin/%s", b, b)

		cmd := exec.Command("git", "fetch", "--force", "origin", ref)
		cmd.Dir = p.repo

		log.Printf("updating branch: %s", b)

		if _, err := cmd.Output(); err != nil {
			log.Printf("unable to fetch branch: %v", err)
			continue
		}
	}
}

func (p *project) writePages(branches []branch) {
	for _, b := range branches {
		log.Printf("processing branch: %s", b)

		go p.writeBranchPage(b)

		for i, c := range b.Commits {
			log.Printf("processing commit: %s: %d/%d", c.Abbr, i+1, len(b.Commits))

			base := filepath.Join(p.base, "commit", c.Hash)

			if err := os.MkdirAll(base, 0755); err != nil {
				if err != nil {
					log.Printf("unable to create commit directory: %v", err)
				}

				continue
			}

			for _, par := range c.Parents {
				p.writeCommitDiff(par, c, base, b)
			}

			for _, obj := range c.Tree {
				dst := filepath.Join(p.base, "object", obj.Dir())

				if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
					if err != nil {
						log.Printf("unable to create object directory: %v", err)
					}
					continue
				}

				p.writeObjectBlob(obj, dst)
				p.writeNom(fmt.Sprintf("%s.html", dst), obj, b, c, base)
			}

			p.writeCommitPage(base, c, b)
		}
	}
}

func (p *project) writeMainIndex(branches []branch) {
	// This is the main index or project home.
	f, err := os.Create(filepath.Join(p.base, "index.html"))

	defer f.Close()

	if err != nil {
		log.Fatalf("unable to create home page: %v", err)
	}

	page := page{
		Data: Data{
			"Branches": branches,
			"Source":   p.options.Source,
			"Project":  p.Name,
		},
		Base:  "./",
		Title: p.Name,
	}

	if err := p.template.Execute(f, page); err != nil {
		log.Fatalf("unable to apply template: %v", err)
	}
}

func (p *project) writeCommitDiff(par string, c commit, base string, b branch) {
	cmd := exec.Command("git", "diff", "-p", fmt.Sprintf("%s..%s", par, c.Hash))
	cmd.Dir = p.repo

	out, err := cmd.Output()

	if err != nil {
		log.Printf("unable to diff against parent: %v", err)

		return
	}

	dst := filepath.Join(base, fmt.Sprintf("diff-%s.html", par))
	f, err := os.Create(dst)

	defer f.Close()

	if err != nil {
		log.Printf("unable to create commit diff to parent: %v", err)

		return
	}

	page := page{
		Data: Data{
			"Diff": diff{
				Body:   fmt.Sprintf("%s", out),
				Commit: c,
				Parent: par,
			},
			"Project": p.Name,
		},
		Base:  "../../",
		Title: strings.Join([]string{p.Name, b.Name, c.Abbr}, ": "),
	}

	if err := p.template.Execute(f, page); err != nil {
		log.Printf("unable to apply template: %v", err)

		return
	}
}

func (p *project) writeBranchPage(b branch) {
	dst := filepath.Join(p.base, "branch", b.Name, "index.html")

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		if err != nil {
			log.Fatalf("unable to create branch directory: %v", err)
		}
		return
	}

	f, err := os.Create(dst)

	defer f.Close()

	if err != nil {
		// TODO: Remove from branches slice?
		log.Printf("unable to create branch page: %v", err)

		return
	}

	page := page{
		Data: Data{
			"Commits": b.Commits,
			"Branch":  b,
			"Project": p.Name,
		},
		Base:  "../../",
		Title: strings.Join([]string{p.Name, b.Name}, ": "),
	}

	if err := p.template.Execute(f, page); err != nil {
		log.Printf("unable to apply template: %v", err)
		return
	}
}

func (p *project) writeObjectBlob(obj object, dst string) {
	cmd := exec.Command("git", "cat-file", "blob", obj.Hash)
	cmd.Dir = p.repo

	out, err := cmd.Output()

	if err != nil {
		log.Printf("unable to save object: %v", err)
		return
	}

	f, err := os.Create(dst)

	defer f.Close()

	if err != nil {
		log.Printf("unable to create object: %v", err)
		return
	}

	if _, err := f.Write(out); err != nil {
		log.Printf("unable to write object blob: %v", err)
		return
	}
}

func (p *project) writeNom(nom string, obj object, b branch, c commit, base string) {
	f, err := os.Create(nom)
	defer f.Close()

	if err != nil {
		log.Printf("unable to create object: %v", err)
		return
	}

	o := &show{
		object: object{
			Hash: obj.Hash,
			Path: obj.Path,
		},
		Bin: types[filepath.Ext(obj.Path)],
	}

	if o.Bin {
		// TODO.
	} else {
		cmd := exec.Command("git", "show", "--no-notes", obj.Hash)
		cmd.Dir = p.repo

		out, err := cmd.Output()

		if err != nil {
			log.Printf("unable to show object: %v", err)

			return
		}

		sep := []byte("\n")
		var lines = make([]int, bytes.Count(out, sep))

		for i := range lines {
			lines[i] = i + 1
		}

		if bytes.LastIndex(out, sep) != len(out)-1 {
			lines = append(lines, len(lines))
		}

		o.Lines = lines
		o.Body = fmt.Sprintf("%s", out)
	}

	page := page{
		Data: Data{
			"Object":  *o,
			"Project": p.Name,
		},
		Base:  "../../",
		Title: strings.Join([]string{p.Name, b.Name, c.Abbr, obj.Path}, ": "),
	}

	if err := p.template.Execute(f, page); err != nil {
		log.Printf("unable to apply template: %v", err)
		return
	}

	lnk := filepath.Join(base, fmt.Sprintf("%s.html", obj.Path))

	if err := os.MkdirAll(filepath.Dir(lnk), 0755); err != nil {
		if err != nil {
			log.Printf("unable to create hard link path: %v", err)
		}
		return
	}

	if err := os.Link(nom, lnk); err != nil {
		if os.IsExist(err) {
			return
		}

		log.Printf("unable to hard link object into commit folder: %v", err)
	}
}

func (p *project) writeCommitPage(base string, c commit, b branch) {
	dst := filepath.Join(base, "index.html")
	f, err := os.Create(dst)

	defer f.Close()

	if err != nil {
		log.Printf("unable to create commit page: %v", err)
		// TODO(spike): handle error?
		return
	}

	page := page{
		Data: Data{
			"Commit":  c,
			"Project": p.Name,
		},
		Base:  "../../",
		Title: strings.Join([]string{p.Name, b.Name, c.Abbr}, ": "),
	}

	if err := p.template.Execute(f, page); err != nil {
		log.Printf("unable to apply template: %v", err)
		return
	}
}
