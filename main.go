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
	"text/tabwriter"
	"time"
)

// For separating out commit line items.
const SEP = "6f6c1745-e902-474a-9e99-08d0084fb011"

//go:embed branch.html.tmpl
var bTmpl string

//go:embed home.html.tmpl
var rTmpl string

//go:embed commit.html.tmpl
var cTmpl string

//go:embed diff.html.tmpl
var dTmpl string

type repository struct {
	base string
	name string
	path string
}

type branch struct {
	Commits []*commit
	Name    string
}

func (b branch) String() string {
	return b.Name
}

type commit struct {
	Graph   string
	Hash    string
	Author  author
	Date    time.Time
	Parent  string
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
	Project  string `json:"project"`
	Quiet    bool   `json:"quiet"`
	Repo     string `json:"repo"`
	URL      string `json:"url"`
}

// Helps store options into a JSON config file.
func (o *options) save(out string) error {
	bs, err := json.MarshalIndent(o, "", "  ")

	if err != nil {
		return fmt.Errorf("unable to encode config file: %v", err)
	}

	if err := os.WriteFile(filepath.Join(out, o.config), bs, 0644); err != nil {
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

	flag.StringVar(&opt.Project, "p", "Jimbo", "Project title")
	flag.StringVar(&opt.Repo, "r", "", "Target repo")
	flag.Var(&opt.Branches, "b", "Target branches")
	flag.StringVar(&opt.URL, "u", "https://host.net/project.git", "Repo public URL")
	flag.BoolVar(&opt.Quiet, "q", false, "Be quiet")
	flag.BoolVar(&opt.Force, "f", false, "Force rebuild")
	flag.Parse()

	if opt.Quiet {
		log.SetOutput(io.Discard)
	}

	cwd, err := os.Getwd()

	if err != nil {
		log.Fatalf("unable to get current working directory: %v", err)
	}

	// Defaults to the current working directory if no argument present.
	out := flag.Arg(0)

	// Make sure `out` is an absolute path.
	if ok := filepath.IsAbs(out); !ok {
		out = filepath.Join(cwd, out)
	}

	// Create a separate options instance for reading config file values into.
	store := *opt

	// Need deep copy the underlying slice types.
	store.Branches = append(store.Branches, opt.Branches...)

	// Attempt to read saved settings.
	bs, err := os.ReadFile(filepath.Join(out, opt.config))

	if err != nil {
		log.Printf("unable to read config file: %v", err)
	}

	// If a config file exists and an option has not been set, override default to match.
	if err := json.Unmarshal(bs, &store); err != nil {
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
	if ok := filepath.IsAbs(opt.Repo); ok {
		// Option considered repo-like if it contains a hidden `.git` dir.
		if _, err := os.Stat(filepath.Join(opt.Repo, ".git")); os.IsNotExist(err) {
			flag.Usage()
			os.Exit(1)
		}
	} else {
		// Allow for URL-looking non-local repos.
		if _, err := url.ParseRequestURI(opt.Repo); err != nil {
			flag.Usage()
			os.Exit(1)
		}
	}

	// Make sure `out` exists.
	if err := os.MkdirAll(out, 0750); err != nil {
		log.Fatalf("unable to create output directory: %v", err)
	}

	// Save current settings for future use.
	if err := opt.save(out); err != nil {
		log.Fatalf("unable to save options: %v", err)
	}

	ucd, err := os.UserCacheDir()

	if err != nil {
		log.Fatalf("unable to locate user cache folder: %s", err)
	}

	p, err := os.MkdirTemp(ucd, "gtx")

	if err != nil {
		log.Fatalf("unable to locate temporary host dir: %s", err)
	}

	repo := &repository{
		base: out,
		name: opt.Project,
		path: p,
	}

	if err := repo.init(opt.Force); err != nil {
		log.Fatalf("unable to initialize output directory: %v", err)
	}

	// Get an up to date copy.
	if err := repo.save(opt.Repo); err != nil {
		log.Fatalf("unable to set up repo: %v", err)
	}

	branches, err := repo.branchfinder(opt.Branches)

	if err != nil {
		log.Fatalf("unable to filter branches: %v", err)
	}

	// Update each branch.
	for _, b := range branches {
		ref := fmt.Sprintf("refs/heads/%s:refs/origin/%s", b, b)

		cmd := exec.Command("git", "fetch", "--force", "origin", ref)
		cmd.Dir = repo.path

		if _, err := cmd.Output(); err != nil {
			log.Printf("unable to fetch branch: %v", err)

			continue
		}
	}

	for _, b := range branches {
		c, err := repo.commitparser(b.Name)

		if err != nil {
			log.Printf("unable to parse %s commit objects: %v", b, err)

			continue
		}

		b.Commits = c
	}

	for _, b := range branches {
		f, err := os.Create(filepath.Join(out, fmt.Sprintf("%s.html", b)))

		defer f.Close()

		if err != nil {
			log.Fatalf("unable to create branch index: %v", err)
		}

		t := template.Must(template.New("branch").Parse(bTmpl))

		if err := t.Execute(f, b); err != nil {
			log.Fatalf("unable to apply branch template: %v", err)
		}
	}

	// NOTE: Why is this even necessary?
	top := branches[0]
	cmd := exec.Command("git", "checkout", filepath.Join("origin", top.Name))
	cmd.Dir = repo.path

	if err := cmd.Run(); err != nil {
		log.Printf("unable to checkout default branch: %v", err)
	}

	// This is the main index or repo home.
	ri, err := os.Create(filepath.Join(out, "index.html"))

	defer ri.Close()

	if err != nil {
		log.Fatalf("unable to create home: %v", err)
	}

	rt := template.Must(template.New("home").Parse(rTmpl))
	rd := struct {
		Branches []*branch
		Link     string
		Project  string
	}{
		Branches: branches,
		Link:     opt.URL,
		Project:  opt.Project,
	}

	if err := rt.Execute(ri, rd); err != nil {
		log.Fatalf("unable to apply home template: %v", err)
	}
}

func (r *repository) init(f bool) error {
	dirs := []string{"branch", "commit", "object"}

	for _, dir := range dirs {
		d := filepath.Join(r.base, dir)

		// Clear existing dirs when -force true.
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

func (r *repository) save(target string) error {
	_, err := os.Stat(r.path)

	if err := exec.Command("git", "clone", target, r.path).Run(); err != nil {
		return err
	}

	// NOTE: Should this be in a separate method?
	cmd := exec.Command("git", "branch", "-l")
	cmd.Dir = r.path
	out, err := cmd.Output()

	if err != nil {
		return err
	}

	all := fmt.Sprintf("%s", out)

	// NOTE: Requires go1.18.
	_, star, found := strings.Cut(all, "*")

	if !found {
		return fmt.Errorf("unable to locate the default branch")
	}

	star = strings.TrimSpace(star)
	star = strings.TrimRight(star, "\n")

	// NOTE: Not sure why this is added in the original.
	// star = filepath.Join("origin", star)

	cmd = exec.Command("git", "checkout", "--detach", star)
	cmd.Dir = r.path
	err = cmd.Run()

	if err != nil {
		return err
	}

	cmd = exec.Command("git", "branch", "-D", star)
	cmd.Dir = r.path
	err = cmd.Run()

	return err
}

func (r *repository) branchfinder(bf manyflag) ([]*branch, error) {
	cmd := exec.Command("git", "branch", "-a")
	cmd.Dir = r.path

	out, err := cmd.Output()

	if err != nil {
		return nil, err
	}

	var results []*branch
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
	if len(bf) > 0 {
		for k := range m {
			m[k] = contains(bf, k)
		}
	}

	// Transfer desired branch names to resulting slice.
	for k, v := range m {
		if v {
			results = append(results, &branch{Name: k})
		}
	}

	return results, nil
}

func (r *repository) commitparser(b string) ([]*commit, error) {
	fst := strings.Join([]string{"%H", "%P", "%s", "%aN", "%aE", "%aD"}, SEP)
	ref := fmt.Sprintf("origin/%s", b)

	cmd := exec.Command("git", "log", fmt.Sprintf("--format=%s", fst), ref)
	cmd.Dir = r.path

	out, err := cmd.Output()

	if err != nil {
		return nil, err
	}

	results := []*commit{}
	scanner := bufio.NewScanner(bytes.NewReader(out))

	for scanner.Scan() {
		data := strings.Split(scanner.Text(), SEP)

		a := author{data[4], data[3]}
		d, err := time.Parse(time.RFC1123Z, data[5])

		if err != nil {
			continue
		}

		c := &commit{
			Author:  a,
			Date:    d,
			Hash:    data[0],
			Parent:  data[1],
			Subject: data[2],
		}

		results = append(results, c)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return results, nil
}
