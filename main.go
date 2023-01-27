package main

import (
	"bufio"
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
	"sync"
	"text/tabwriter"
	"time"
)

// SEP is a UUID v4 used to separate out commit line items.
const SEP = "6f6c1745-e902-474a-9e99-08d0084fb011"

//go:embed page.html.tmpl
var tpl string

type project struct {
	base     string
	Branches []branch
	Name     string
	repo     string
}

type page struct {
	Breadcrumbs []string
	Data        map[string]interface{}
	Title       string
}

type branch struct {
	Commits []commit
	Name    string
	Project string
}

func (b branch) String() string {
	return b.Name
}

type hash struct {
	Hash  string
	Short string
}

func (h hash) String() string {
	return h.Hash
}

type object struct {
	Hash string
	Path string
}

type commit struct {
	Branch  string
	Body    string
	Abbr    string
	History []string
	Parents []string
	Graph   string
	Hash    string
	Author  author
	Date    time.Time
	Project string
	Tree    []object
	Subject string
}

type author struct {
	Email string
	Name  string
}

// https://stackoverflow.com/questions/28322997/how-to-get-a-list-of-values-into-a-flag-in-golang/
type manyflag []string

func (f *manyflag) Set(value string) error {
	// Make sure there are no duplicates.
	if !contains(*f, value) {
		*f = append(*f, value)
	}

	return nil
}

func (f *manyflag) String() string {
	return strings.Join(*f, ", ")
}

type options struct {
	Branches manyflag `json:"branches"`
	config   string
	Force    bool   `json:"force"`
	Name     string `json:"name"`
	Quiet    bool   `json:"quiet"`
	Source   string `json:"source"`
	Template string `json:"template"`
	URL      string `json:"url"`
}

// Helps store options into a JSON config file.
func (o *options) save(p string) error {
	bs, err := json.MarshalIndent(o, "", "  ")

	if err != nil {
		return fmt.Errorf("unable to encode config file: %v", err)
	}

	if err := os.WriteFile(filepath.Join(p, o.config), bs, 0644); err != nil {
		return fmt.Errorf("unable to save config file: %v", err)
	}

	return nil
}

func init() {
	// Override default usage output.
	flag.Usage = func() {
		// Print usage example ahead of lisiting default options.
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

	log.Print(flagset)

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
	if err := os.MkdirAll(dir, 0750); err != nil {
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

	prj := &project{
		base: dir,
		Name: opt.Name,
		repo: tmp,
	}

	// Create base directories.
	if err := prj.init(opt.Force); err != nil {
		log.Fatalf("unable to initialize output directory: %v", err)
	}

	// Clone target repo.
	if err := prj.save(opt.Source); err != nil {
		log.Fatalf("unable to set up repo: %v", err)
	}

	branches, err := prj.branchfilter(opt.Branches)

	if err != nil {
		log.Fatalf("unable to filter branches: %v", err)
	}

	var wg sync.WaitGroup

	// Update each branch.
	for _, b := range branches {
		// NOTE: Is this needed still if the repo is downloaded each time the script is run?
		ref := fmt.Sprintf("refs/heads/%s:refs/origin/%s", b, b)

		cmd := exec.Command("git", "fetch", "--force", "origin", ref)
		cmd.Dir = prj.repo

		if _, err := cmd.Output(); err != nil {
			log.Printf("unable to fetch branch: %v", err)

			continue
		}

		log.Printf("processing branch: %s", b)
		wg.Add(1)

		go func() {
			defer wg.Done()

			dst := filepath.Join(prj.base, "branch", b.Name, "index.html")

			if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
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

			t := template.Must(template.New("branch").Parse(tpl))
			p := page{
				Data: map[string]interface{}{
					"Commits": b.Commits,
					"Project": prj.Name,
				},
				Title: strings.Join([]string{prj.Name, b.Name}, ": "),
			}

			if err := t.Execute(f, p); err != nil {
				log.Printf("unable to apply template: %v", err)

				return
			}
		}()
	}

	for _, b := range branches {
		for i, c := range b.Commits {
			log.Printf("processing commit: %s: %d/%d", c.Abbr, i+1, len(b.Commits))

			base := filepath.Join(prj.base, "commit", c.Hash)

			if err := os.MkdirAll(base, 0750); err != nil {
				if err != nil {
					log.Printf("unable to create commit directory: %v", err)
				}

				continue
			}

			for _, psh := range c.Parents {
				wg.Add(1)

				go func() {
					defer wg.Done()

					// NOTE: Use <em>, <ins>, and <del> instead of blue, green, red <font> elements
					cmd := exec.Command("git", "diff", "-p", fmt.Sprintf("%s..%s", psh, c.Hash))
					cmd.Dir = prj.repo

					out, err := cmd.Output()

					if err != nil {
						log.Printf("unable to diff against parent: %v", err)

						return
					}

					dst := filepath.Join(base, fmt.Sprintf("diff-to-%s.html", psh))
					f, err := os.Create(dst)

					defer f.Close()

					if err != nil {
						log.Printf("unable to create commit diff to parent page: %v", err)

						return
					}

					t := template.Must(template.New("diff").Parse(tpl))
					p := page{
						Data: map[string]interface{}{
							"Diff": struct {
								Body   string
								Commit commit
								Parent string
							}{
								Body:   fmt.Sprintf("%s", out),
								Commit: c,
								Parent: psh,
							},
							"Project": prj.Name,
						},
						Title: strings.Join([]string{prj.Name, b.Name, c.Abbr}, ": "),
					}

					if err := t.Execute(f, p); err != nil {
						log.Printf("unable to apply template: %v", err)

						return
					}
				}()
			}

			for _, obj := range c.Tree {
				dst := filepath.Join(prj.base, "object", obj.Hash[0:2], obj.Hash)

				if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
					if err != nil {
						log.Printf("unable to create object directory: %v", err)
					}

					continue
				}

				func(name string) {
					cmd := exec.Command("git", "show", "--no-notes", obj.Hash)
					cmd.Dir = prj.repo

					out, err := cmd.Output()

					if err != nil {
						log.Printf("unable to show object: %v", err)

						return
					}

					f, err := os.Create(name)

					defer f.Close()

					if err != nil {
						log.Printf("unable to create object: %v", err)

						return
					}

					var lines = make([]int, bytes.Count(out, []byte("\n")))

					for i := range lines {
						lines[i] = i + 1
					}

					t := template.Must(template.New("object").Parse(tpl))
					p := page{
						Data: map[string]interface{}{
							"Object": struct {
								Body  string
								Hash  string
								Lines []int
							}{
								Body:  fmt.Sprintf("%s", out),
								Hash:  obj.Hash,
								Lines: lines,
							},
							"Project": prj.Name,
						},
						Title: strings.Join([]string{prj.Name, b.Name, c.Abbr, obj.Path}, ": "),
					}

					if err := t.Execute(f, p); err != nil {
						log.Printf("unable to apply template: %v", err)

						return
					}

					lnk := filepath.Join(base, fmt.Sprintf("%s.html", obj.Path))

					if err := os.MkdirAll(filepath.Dir(lnk), 0750); err != nil {
						if err != nil {
							log.Printf("unable to create hard link path: %v", err)
						}

						return
					}

					if err := os.Link(name, lnk); err != nil {
						if os.IsExist(err) {
							return
						}

						log.Printf("unable to hard link object into commit folder: %v", err)
					}
				}(fmt.Sprintf("%s.html", dst))

				func(name string) {
					cmd := exec.Command("git", "cat-file", "blob", obj.Hash)
					cmd.Dir = prj.repo

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
				}(dst)
			}

			wg.Add(1)

			go func() {
				defer wg.Done()

				dst := filepath.Join(base, "index.html")
				f, err := os.Create(dst)

				defer f.Close()

				if err != nil {
					log.Printf("unable to create commit page: %v", err)

					return
				}

				t := template.Must(template.New("commit").Parse(tpl))
				p := page{
					Data: map[string]interface{}{
						"Commit":  c,
						"Project": prj.Name,
					},
					Title: strings.Join([]string{prj.Name, b.Name, c.Abbr}, ": "),
				}

				if err := t.Execute(f, p); err != nil {
					log.Printf("unable to apply template: %v", err)

					return
				}
			}()
		}
	}

	wg.Add(1)

	go func() {
		defer wg.Done()

		// This is the main index or project home.
		f, err := os.Create(filepath.Join(prj.base, "index.html"))

		defer f.Close()

		if err != nil {
			log.Fatalf("unable to create home page: %v", err)
		}

		t := template.Must(template.New("home").Parse(tpl))
		p := page{
			Data: map[string]interface{}{
				"Branches": branches,
				"Link":     opt.URL,
				"Project":  prj.Name,
			},
			Title: prj.Name,
		}

		if err := t.Execute(f, p); err != nil {
			log.Fatalf("unable to apply template: %v", err)
		}
	}()

	wg.Wait()
}

// Creates base directories for holding objects, branches, and commits.
func (prj *project) init(f bool) error {
	dirs := []string{"branch", "commit", "object"}

	for _, dir := range dirs {
		d := filepath.Join(prj.base, dir)

		// Clear existing dirs when -f true.
		if f && dir != "branch" {
			if err := os.RemoveAll(d); err != nil {
				return fmt.Errorf("unable to remove directory: %v", err)
			}
		}

		if err := os.MkdirAll(d, 0750); err != nil {
			return fmt.Errorf("unable to create directory: %v", err)
		}
	}

	return nil
}

// Saves a local clone of `target` repo.
func (prj *project) save(target string) error {
	if _, err := os.Stat(prj.repo); err != nil {
		return err
	}

	return exec.Command("git", "clone", target, prj.repo).Run()
}

// Goes through list of branches and returns those that match whitelist.
func (prj *project) branchfilter(whitelist manyflag) ([]branch, error) {
	cmd := exec.Command("git", "branch", "-a")
	cmd.Dir = prj.repo

	out, err := cmd.Output()

	if err != nil {
		return nil, err
	}

	var m = make(map[string]bool)

	scanner := bufio.NewScanner(bytes.NewReader(out))

	for scanner.Scan() {
		t := strings.TrimSpace(scanner.Text())
		_, f := filepath.Split(t)

		m[f] = !strings.Contains(f, "HEAD")
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Filter to match options, but return all if no branch flags given.
	if len(whitelist) > 0 {
		for k := range m {
			m[k] = contains(whitelist, k)
		}
	}

	// Fill in resulting slice with desired branches.
	var results []branch

	for k, v := range m {
		if v {
			commits, err := prj.commitparser(k)

			if err != nil {
				continue
			}

			results = append(results, branch{commits, k, prj.Name})
		}
	}

	return results, nil
}

func (prj *project) commitparser(b string) ([]commit, error) {
	fst := strings.Join([]string{"%H", "%P", "%s", "%aN", "%aE", "%aD", "%h"}, SEP)
	ref := fmt.Sprintf("origin/%s", b)

	cmd := exec.Command("git", "log", "--graph", fmt.Sprintf("--format=%s", fst), ref)
	cmd.Dir = prj.repo

	out, err := cmd.Output()

	if err != nil {
		return nil, err
	}

	results := []commit{}
	scanner := bufio.NewScanner(bytes.NewReader(out))

	for scanner.Scan() {
		data := strings.Split(scanner.Text(), SEP)

		k := strings.Split(data[0], " ")
		g, h := k[0], k[1]
		a := author{data[4], data[3]}

		date, err := time.Parse("Mon, 2 Jan 2006 15:04:05 -0700", data[5])

		if err != nil {
			log.Printf("unable to parse commit date: %s", err)

			continue
		}

		body, err := prj.bodyparser(h)

		if err != nil {
			log.Printf("unable to parse commit body: %s", err)

			continue
		}

		tree, err := prj.treeparser(h)

		if err != nil {
			log.Printf("unable to parse commit tree: %s", err)

			continue
		}

		var history []string
		var parents []string

		if data[1] != "" {
			parents = strings.Split(data[1], " ")
		}

		for _, p := range parents {
			diffstat, err := prj.diffparser(h, p)

			if err != nil {
				log.Printf("unable to diff stat against parent: %s", err)

				continue
			}

			history = append(history, diffstat)
		}

		c := commit{
			Abbr:    data[6],
			Author:  a,
			Branch:  b,
			Body:    body,
			Date:    date,
			Hash:    h,
			History: history,
			Tree:    tree,
			Graph:   g,
			Parents: parents,
			Project: prj.Name,
			Subject: data[2],
		}

		results = append(results, c)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

func (prj *project) treeparser(h string) ([]object, error) {
	// git ls-tree --format='%(objectname) %(path)' <tree-ish>
	cmd := exec.Command("git", "ls-tree", "-r", "--format=%(objectname) %(path)", h)
	cmd.Dir = prj.repo

	out, err := cmd.Output()

	if err != nil {
		return nil, err
	}

	var results []object
	feed := strings.Split(strings.TrimSuffix(fmt.Sprintf("%s", out), "\n"), "\n")

	for _, line := range feed {
		w := strings.Split(line, " ")

		results = append(results, object{
			Hash: w[0],
			Path: w[1],
		})
	}

	return results, nil
}

func (prj *project) diffparser(h, p string) (string, error) {
	// histo, file, changes, sum
	cmd := exec.Command("git", "diff", "--stat", fmt.Sprintf("%s..%s", p, h))
	cmd.Dir = prj.repo

	out, err := cmd.Output()

	if err != nil {
		return "", err
	}

	var results []string
	feed := strings.Split(strings.TrimSuffix(fmt.Sprintf("%s", out), "\n"), "\n")

	for i, line := range feed {
		if i < len(feed) {
			// TODO: Parse filenames and stats.
		} else {
			// Last line needs no parsing.
		}

		results = append(results, strings.TrimSpace(line))
	}

	return strings.Join(results, "\n"), nil
}

func (prj *project) bodyparser(h string) (string, error) {
	// Because the commit message body is multiline and is tripping the scanner.
	cmd := exec.Command("git", "show", "--no-patch", "--format=%B", h)
	cmd.Dir = prj.repo

	out, err := cmd.Output()

	if err != nil {
		return "", err
	}

	return strings.TrimSuffix(fmt.Sprintf("%s", out), "\n"), nil
}
