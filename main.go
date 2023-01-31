package main

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"text/tabwriter"
)

// EMPTY is git's magic empty tree hash.
const EMPTY = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

//go:embed page.html.tmpl
var tpl string

func init() {
	// Override default usage output.
	flag.Usage = func() {
		// Print usage example ahead of listing default options.
		fmt.Fprintln(flag.CommandLine.Output(), "usage:", os.Args[0], "[<options>] <path>")
		flag.PrintDefaults()
	}

	// Swap default logger timestamps for a custom prefix.
	log.SetFlags(log.Lmsgprefix)
	log.SetPrefix("gtx: ")
}

func main() {
	opt := &options{
		config: ".jimmy.json",
	}

	// NOTE: Flags need match each option key's first letter.
	flag.StringVar(&opt.Name, "n", "Jimbo", "Project title")
	flag.StringVar(&opt.Source, "s", "", "Source repository")
	flag.Var(&opt.Branches, "b", "Target branches")
	flag.StringVar(&opt.Template, "t", "", "Page template")
	flag.StringVar(&opt.URL, "u", "https://host.net/project.git", "Source URL")
	flag.BoolVar(&opt.Quiet, "q", false, "Be quiet")
	flag.BoolVar(&opt.Force, "f", false, "Force rebuild")
	flag.Parse()

	if opt.Quiet {
		log.SetOutput(io.Discard)
	}

	if opt.Template != "" {
		bs, err := os.ReadFile(opt.Template)

		if err != nil {
			log.Printf("unable to read template: %v", err)
		} else {
			tpl = string(bs)
		}
	}

	cwd, err := os.Getwd()

	if err != nil {
		log.Fatalf("unable to get current working directory: %v", err)
	}

	// Defaults to the current working directory if no argument present.
	dir := flag.Arg(0)

	// Make sure `dir` is an absolute path.
	if ok := filepath.IsAbs(dir); !ok {
		dir = filepath.Join(cwd, dir)
	}

	// Create a separate options instance for reading config file values into.
	store := *opt

	// Need deep copy the underlying slice types.
	store.Branches = append(store.Branches, opt.Branches...)

	// Attempt to read saved settings.
	cnf, err := os.ReadFile(filepath.Join(dir, opt.config))

	if err != nil {
		log.Printf("unable to read config file: %v", err)
	}

	// If a config file exists and an option has not been set, override default to match.
	if err := json.Unmarshal(cnf, &store); err != nil {
		log.Printf("unable to parse config file: %v", err)
	}

	// Collect flags provided.
	flagset := make(map[string]bool)

	// NOTE: These need to come before the output directory argument.
	flag.Visit(func(f *flag.Flag) {
		flagset[f.Name] = true
	})

	ref := reflect.ValueOf(store)
	tab := tabwriter.NewWriter(log.Writer(), 0, 0, 0, '.', 0)

	flag.VisitAll(func(f *flag.Flag) {
		// Attempt to source settings from config file, then override flag defaults.
		if !flagset[f.Name] {
			v := ref.FieldByNameFunc(func(n string) bool {
				return strings.HasPrefix(strings.ToLower(n), f.Name)
			})

			// Don't ask.
			if s, ok := v.Interface().(manyflag); ok {
				for _, b := range s {
					flag.Set(f.Name, b)
				}
			} else {
				// This has the welcome side effect of magically overriding `opt` fields.
				flag.Set(f.Name, v.String())
			}
		}

		fmt.Fprintf(tab, "gtx: -%s \t%s\t: %v\n", f.Name, f.Usage, f.Value)
	})

	tab.Flush()

	// The repo flag is required at this point.
	if ok := filepath.IsAbs(opt.Source); ok {
		// Option considered repo-like if it contains a hidden `.git` dir.
		if _, err := os.Stat(filepath.Join(opt.Source, ".git")); os.IsNotExist(err) {
			flag.Usage()
			os.Exit(1)
		}
	} else {
		// Allow for URL-looking non-local repos.
		if _, err := url.ParseRequestURI(opt.Source); err != nil {
			flag.Usage()
			os.Exit(1)
		}
	}

	// Make sure `dir` exists.
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Fatalf("unable to create output directory: %v", err)
	}

	// Save current settings for future use.
	if err := opt.save(dir); err != nil {
		log.Fatalf("unable to save options: %v", err)
	}

	ucd, err := os.UserCacheDir()

	if err != nil {
		log.Fatalf("unable to locate user cache folder: %s", err)
	}

	tmp, err := os.MkdirTemp(ucd, "gtx-*")

	if err != nil {
		log.Fatalf("unable to locate temporary host dir: %s", err)
	}

	log.Printf("user cache set: %s", tmp)

	pro := &project{
		base: dir,
		Name: opt.Name,
		repo: tmp,
	}

	// Create base directories.
	if err := pro.init(opt.Force); err != nil {
		log.Fatalf("unable to initialize output directory: %v", err)
	}

	// Clone target repo.
	if err := pro.save(opt.Source); err != nil {
		log.Fatalf("unable to set up repo: %v", err)
	}

	branches, err := pro.branchfilter(opt.Branches)

	if err != nil {
		log.Fatalf("unable to filter branches: %v", err)
	}

	t := template.Must(template.New("page").Funcs(template.FuncMap{
		"diffstatbodyparser": diffstatbodyparser,
		"diffbodyparser":     diffbodyparser,
	}).Parse(tpl))

	updateBranches(branches, pro)
	writePages(branches, pro, t)
	writeMainIndex(pro, opt, t, branches)
}

func updateBranches(branches []branch, pro *project) {
	for _, b := range branches {
		// NOTE: Is this needed still if the repo is downloaded each time the script is run?
		ref := fmt.Sprintf("refs/heads/%s:refs/origin/%s", b, b)

		cmd := exec.Command("git", "fetch", "--force", "origin", ref)
		cmd.Dir = pro.repo

		log.Printf("updating branch: %s", b)

		if _, err := cmd.Output(); err != nil {
			log.Printf("unable to fetch branch: %v", err)
			continue
		}
	}
}


func writePages(branches []branch, pro *project, t *template.Template) {
	for _, b := range branches {
		log.Printf("processing branch: %s", b)

		go writeBranchPage(pro, b, t)

		for i, c := range b.Commits {
			log.Printf("processing commit: %s: %d/%d", c.Abbr, i+1, len(b.Commits))

			base := filepath.Join(pro.base, "commit", c.Hash)

			if err := os.MkdirAll(base, 0755); err != nil {
				if err != nil {
					log.Printf("unable to create commit directory: %v", err)
				}

				continue
			}

			for _, par := range c.Parents {
				writeCommitDiff(par, c, pro, base, b, t)
			}

			for _, obj := range c.Tree {
				dst := filepath.Join(pro.base, "object", obj.Dir())

				if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
					if err != nil {
						log.Printf("unable to create object directory: %v", err)
					}
					continue
				}

				writeObjectBlob(obj, pro, dst)
				writeNom(fmt.Sprintf("%s.html", dst), obj, pro, b, c, t, base)
			}

			writeCommitPage(base, pro, c, b, t)
		}
	}
}

func writeMainIndex(pro *project, opt *options, t *template.Template, branches []branch) {
	// This is the main index or project home.
	f, err := os.Create(filepath.Join(pro.base, "index.html"))

	defer f.Close()

	if err != nil {
		log.Fatalf("unable to create home page: %v", err)
	}

	p := page{
		Data: Data{
			"Branches": branches,
			"Link":     opt.URL,
			"Project":  pro.Name,
		},
		Base:  "./",
		Title: pro.Name,
	}

	if err := t.Execute(f, p); err != nil {
		log.Fatalf("unable to apply template: %v", err)
	}
}

func writeCommitDiff(par string, c commit, pro *project, base string, b branch, t *template.Template) {
	cmd := exec.Command("git", "diff", "-p", fmt.Sprintf("%s..%s", par, c.Hash))
	cmd.Dir = pro.repo

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

	p := page{
		Data: Data{
			"Diff": diff{
				Body:   fmt.Sprintf("%s", out),
				Commit: c,
				Parent: par,
			},
			"Project": pro.Name,
		},
		Base:  "../../",
		Title: strings.Join([]string{pro.Name, b.Name, c.Abbr}, ": "),
	}

	if err := t.Execute(f, p); err != nil {
		log.Printf("unable to apply template: %v", err)

		return
	}
}

func writeBranchPage(pro *project, b branch, t *template.Template) {
	dst := filepath.Join(pro.base, "branch", b.Name, "index.html")

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

	p := page{
		Data: Data{
			"Commits": b.Commits,
			"Branch":  b,
			"Project": pro.Name,
		},
		Base:  "../../",
		Title: strings.Join([]string{pro.Name, b.Name}, ": "),
	}

	if err := t.Execute(f, p); err != nil {
		log.Printf("unable to apply template: %v", err)
		return
	}
}

func writeObjectBlob(obj object, pro *project, dst string) {
	cmd := exec.Command("git", "cat-file", "blob", obj.Hash)
	cmd.Dir = pro.repo

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

func writeNom(nom string, obj object, pro *project, b branch, c commit, t *template.Template, base string) {
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
		cmd.Dir = pro.repo

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

	p := page{
		Data: Data{
			"Object":  *o,
			"Project": pro.Name,
		},
		Base:  "../../",
		Title: strings.Join([]string{pro.Name, b.Name, c.Abbr, obj.Path}, ": "),
	}

	if err := t.Execute(f, p); err != nil {
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

func writeCommitPage(base string, pro *project, c commit, b branch, t *template.Template) {
	dst := filepath.Join(base, "index.html")
	f, err := os.Create(dst)

	defer f.Close()

	if err != nil {
		log.Printf("unable to create commit page: %v", err)
		// TODO(spike): handle error?
		return
	}

	p := page{
		Data: Data{
			"Commit":  c,
			"Project": pro.Name,
		},
		Base:  "../../",
		Title: strings.Join([]string{pro.Name, b.Name, c.Abbr}, ": "),
	}

	if err := t.Execute(f, p); err != nil {
		log.Printf("unable to apply template: %v", err)
		return
	}
}
