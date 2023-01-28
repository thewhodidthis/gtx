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
	"regexp"
	"strings"
	"sync"
	"text/tabwriter"
	"time"
)

// EMPTY is git's magic empty tree hash.
const EMPTY = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// SEP is a browser generated UUID v4 used to separate out commit line items.
const SEP = "6f6c1745-e902-474a-9e99-08d0084fb011"

//go:embed page.html.tmpl
var tpl string

// Match diff body keywords.
var xline = regexp.MustCompile(`^(deleted|index|new|rename|similarity)`)

// Match diff body @@ del, ins line numbers.
var aline = regexp.MustCompile(`\-(.*?),`)
var bline = regexp.MustCompile(`\+(.*?),`)

// Helps target file specific diff blocks.
var diffanchor = regexp.MustCompile(`b\/(.*?)$`)

// Helps keep track of file extensions git thinks of as binary.
var types = make(map[string]bool)

type project struct {
	base     string
	Branches []branch
	Name     string
	repo     string
}

// Data is the generic content map passed on to the page template.
type Data map[string]interface{}
type page struct {
	Data
	Base       string
	Stylesheet string
	Title      string
}

type branch struct {
	Commits []commit
	Name    string
	Project string
}

func (b branch) String() string {
	return b.Name
}

type diff struct {
	Body   string
	Commit commit
	Parent string
}

type overview struct {
	Body   string
	Hash   string
	Parent string
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

func (o object) Dir() string {
	return filepath.Join(o.Hash[0:2], o.Hash[2:])
}

type show struct {
	Body  string
	Bin   bool
	Lines []int
	object
}

type commit struct {
	Branch  string
	Body    string
	Abbr    string
	History []overview
	Parents []string
	Hash    string
	Author  author
	Date    time.Time
	Project string
	Tree    []object
	Types   map[string]bool
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

	var wg sync.WaitGroup
	t := template.Must(template.New("page").Funcs(template.FuncMap{
		"diffstatbodyparser": diffstatbodyparser,
		"diffbodyparser":     diffbodyparser,
	}).Parse(tpl))

	// Update each branch.
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

	for _, b := range branches {
		log.Printf("processing branch: %s", b)
		wg.Add(1)

		go func() {
			defer wg.Done()

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
		}()

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
				wg.Add(1)

				go func() {
					defer wg.Done()

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
				}()
			}

			for _, obj := range c.Tree {
				dst := filepath.Join(pro.base, "object", obj.Dir())

				if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
					if err != nil {
						log.Printf("unable to create object directory: %v", err)
					}

					continue
				}

				func() {
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
				}()

				func(nom string) {
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
				}(fmt.Sprintf("%s.html", dst))
			}

			func() {
				dst := filepath.Join(base, "index.html")
				f, err := os.Create(dst)

				defer f.Close()

				if err != nil {
					log.Printf("unable to create commit page: %v", err)

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
			}()
		}
	}

	wg.Add(1)

	go func() {
		defer wg.Done()

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
	}()

	wg.Wait()
}

// Creates base directories for holding objects, branches, and commits.
func (pro *project) init(f bool) error {
	dirs := []string{"branch", "commit", "object"}

	for _, dir := range dirs {
		d := filepath.Join(pro.base, dir)

		// Clear existing dirs when -f true.
		if f && dir != "branch" {
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
func (pro *project) save(target string) error {
	if _, err := os.Stat(pro.repo); err != nil {
		return err
	}

	return exec.Command("git", "clone", target, pro.repo).Run()
}

// Goes through list of branches and returns those that match whitelist.
func (pro *project) branchfilter(whitelist manyflag) ([]branch, error) {
	cmd := exec.Command("git", "branch", "-a")
	cmd.Dir = pro.repo

	out, err := cmd.Output()

	if err != nil {
		return nil, err
	}

	var b = make(map[string]branch)
	var m = make(map[string]bool)

	scanner := bufio.NewScanner(bytes.NewReader(out))

	for scanner.Scan() {
		t := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "*"))
		_, f := filepath.Split(t)

		m[f] = true
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Filter to match options, but return all if no branch flags given.
	if len(whitelist) > 0 {
		for k := range m {
			m[k] = contains(whitelist, k)
		}
	} else {
		// In git given order at this point.
		for k := range m {
			whitelist = append(whitelist, k)
		}
	}

	for k, v := range m {
		if v {
			// TODO: Try a goroutine?
			commits, err := pro.commitparser(k)

			if err != nil {
				continue
			}

			b[k] = branch{commits, k, pro.Name}
		}
	}

	// Fill in resulting slice with desired branches in order.
	var results []branch

	for _, v := range whitelist {
		results = append(results, b[v])
	}

	return results, nil
}

func (pro *project) commitparser(b string) ([]commit, error) {
	fst := strings.Join([]string{"%H", "%P", "%s", "%aN", "%aE", "%aD", "%h"}, SEP)
	ref := fmt.Sprintf("origin/%s", b)

	cmd := exec.Command("git", "log", fmt.Sprintf("--format=%s", fst), ref)
	cmd.Dir = pro.repo

	out, err := cmd.Output()

	if err != nil {
		return nil, err
	}

	results := []commit{}
	scanner := bufio.NewScanner(bytes.NewReader(out))

	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		data := strings.Split(text, SEP)

		h := data[0]

		var history []overview
		var parents []string

		if data[1] != "" {
			parents = strings.Split(data[1], " ")
		}

		for _, p := range parents {
			diffstat, err := pro.diffstatparser(h, p)

			if err != nil {
				log.Printf("unable to diffstat against parent: %s", err)

				continue
			}

			history = append(history, overview{diffstat, h, p})
		}

		a := author{data[4], data[3]}

		date, err := time.Parse("Mon, 2 Jan 2006 15:04:05 -0700", data[5])

		if err != nil {
			log.Printf("unable to parse commit date: %s", err)

			continue
		}

		body, err := pro.bodyparser(h)

		if err != nil {
			log.Printf("unable to parse commit body: %s", err)

			continue
		}

		tree, err := pro.treeparser(h)

		if err != nil {
			log.Printf("unable to parse commit tree: %s", err)

			continue
		}

		c := commit{
			Abbr:    data[6],
			Author:  a,
			Body:    body,
			Branch:  b,
			Date:    date,
			Hash:    h,
			History: history,
			Parents: parents,
			Project: pro.Name,
			Subject: data[2],
			Tree:    tree,
		}

		results = append(results, c)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return results, nil
}

func (pro *project) treeparser(h string) ([]object, error) {
	cmd := exec.Command("git", "ls-tree", "-r", "--format=%(objectname) %(path)", h)
	cmd.Dir = pro.repo

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

func (pro *project) diffstatparser(h, p string) (string, error) {
	cmd := exec.Command("git", "diff", "--stat", fmt.Sprintf("%s..%s", p, h))
	cmd.Dir = pro.repo

	out, err := cmd.Output()

	if err != nil {
		return "", err
	}

	var results []string
	feed := strings.Split(strings.TrimSuffix(fmt.Sprintf("%s", out), "\n"), "\n")

	for _, line := range feed {
		// NOTE: This is hackish I know, attach to project?
		i := strings.Index(line, "|")

		if i != -1 {
			ext := filepath.Ext(strings.TrimSpace(line[:i]))
			types[ext] = strings.Contains(line, "Bin")
		}

		results = append(results, strings.TrimSpace(line))
	}

	return strings.Join(results, "\n"), nil
}

func (pro *project) bodyparser(h string) (string, error) {
	// Because the commit message body is multiline and is tripping the scanner.
	cmd := exec.Command("git", "show", "--no-patch", "--format=%B", h)
	cmd.Dir = pro.repo

	out, err := cmd.Output()

	if err != nil {
		return "", err
	}

	return strings.TrimSuffix(fmt.Sprintf("%s", out), "\n"), nil
}

func diffbodyparser(d diff) template.HTML {
	var results []string
	feed := strings.Split(strings.TrimSuffix(template.HTMLEscapeString(d.Body), "\n"), "\n")

	var a, b string

	for _, line := range feed {
		if strings.HasPrefix(line, "diff") {
			line = diffanchor.ReplaceAllString(line, `b/<a id="$1">$1</a>`)
			line = fmt.Sprintf("<strong>%s</strong>", line)
		}

		line = xline.ReplaceAllString(line, "<em>$1</em>")

		if strings.HasPrefix(line, "@@") {
			if a != "" && !strings.HasPrefix(a, "---") {
				repl := fmt.Sprintf(`<a href="commit/%s/%s.html#L$1">-$1</a>,`, d.Parent, a)
				line = aline.ReplaceAllString(line, repl)
			}

			if b != "" && !strings.HasPrefix(b, "+++") {
				repl := fmt.Sprintf(`<a href="commit/%s/%s.html#L$1">+$1</a>,`, d.Commit.Hash, b)
				line = bline.ReplaceAllString(line, repl)
			}
		}

		if strings.HasPrefix(line, "---") {
			a = strings.TrimPrefix(line, "--- a/")
			line = fmt.Sprintf("<mark>%s</mark>", line)
		} else if strings.HasPrefix(line, "-") {
			line = fmt.Sprintf("<del>%s</del>", line)
		}

		if strings.HasPrefix(line, "+++") {
			b = strings.TrimPrefix(line, "+++ b/")
			line = fmt.Sprintf("<mark>%s</mark>", line)
		} else if strings.HasPrefix(line, "+") {
			line = fmt.Sprintf("<ins>%s</ins>", line)
		}

		results = append(results, line)
	}

	return template.HTML(strings.Join(results, "\n"))
}

func diffstatbodyparser(o overview) template.HTML {
	var results []string
	feed := strings.Split(strings.TrimSuffix(o.Body, "\n"), "\n")

	for i, line := range feed {
		if i < len(feed)-1 {
			// Link files to corresponding diff.
			columns := strings.Split(line, "|")
			files := strings.Split(columns[0], "=>")

			a := strings.TrimSpace(files[len(files)-1])
			b := fmt.Sprintf(`<a href="commit/%s/diff-%s.html#%s">%s</a>`, o.Hash, o.Parent, a, a)
			l := strings.LastIndex(line, a)

			line = line[:l] + strings.Replace(line[l:], a, b, 1)
		}

		results = append(results, line)
	}

	return template.HTML(strings.Join(results, "\n"))
}
