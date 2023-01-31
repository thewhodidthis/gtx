package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
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

	// NOTE: Flags need to match each option key's first letter.
	flag.StringVar(&opt.Name, "n", "Jimbo", "Project title")
	flag.StringVar(&opt.Source, "s", "", "Source repository")
	flag.Var(&opt.Branches, "b", "Target branches")
	flag.StringVar(&opt.Template, "t", "", "Page template")
	flag.BoolVar(&opt.Quiet, "q", false, "Be quiet")
	flag.BoolVar(&opt.Export, "e", false, "Export default template")
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
	dir := flag.Arg(0)

	// Make sure `dir` is an absolute path.
	if ok := filepath.IsAbs(dir); !ok {
		dir = filepath.Join(cwd, dir)
	}

	if opt.Export {
		if err := os.WriteFile(filepath.Join(dir, "page.html.tmpl"), []byte(tpl), 0644); err != nil {
			log.Fatalf("unable to export default template: %v", err)
		}

		log.Printf("done exporting default template")
		return
	}

	if opt.Template != "" {
		bs, err := os.ReadFile(opt.Template)

		if err != nil {
			log.Printf("unable to read template: %v", err)
		} else {
			tpl = string(bs)
		}
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

	pro := NewProject(dir, tmp, opt)

	// Create base directories.
	if err := pro.init(); err != nil {
		log.Fatalf("unable to initialize output directory: %v", err)
	}

	// Clone target repo.
	if err := pro.save(); err != nil {
		log.Fatalf("unable to set up repo: %v", err)
	}

	branches, err := branchFilter(tmp, opt)
	if err != nil {
		log.Fatalf("unable to filter branches: %v", err)
	}

	pro.updateBranches(branches)

	pro.writePages(branches)
	pro.writeMainIndex(branches)
}
