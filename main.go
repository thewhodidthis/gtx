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
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"text/tabwriter"
)

//go:embed branch.html.tmpl
var bTmpl string

//go:embed home.html.tmpl
var rTmpl string

//go:embed commit.html.tmpl
var cTmpl string

//go:embed diff.html.tmpl
var dTmpl string

type repo struct {
	name string
	base string
	path string
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
	Force    bool     `json:"force"`
	Project  string   `json:"project"`
	Quiet    bool     `json:"quiet"`
	Repo     string   `json:"repo"`
	URL      string   `json:"url"`
	config   string
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

	p, err := ioutil.TempDir(ucd, "gtx-*")

	if err != nil {
		log.Fatalf("unable to locate temporary host dir: %s", err)
	}

	repo := &repo{
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

	branches, err := repo.findBranches(opt.Branches)

	if err != nil {
		log.Fatalf("unable to filter branches: %v", err)
	}

	for _, b := range branches {
		ref := fmt.Sprintf("refs/heads/%s:refs/origin/%s", b, b)

		cmd := exec.Command("git", "fetch", "--force", "origin", ref)
		cmd.Dir = repo.path

		if _, err := cmd.Output(); err != nil {
			log.Printf("unable to fetch branch: %v", err)

			continue
		}
	}

	// NOTE: Why is this even necessary?
	cmd := exec.Command("git", "checkout", filepath.Join("origin", branches[0]))
	cmd.Dir = repo.path

	if err := cmd.Run(); err != nil {
		log.Printf("unable to checkout default branch: %v", err)
	}

	// This is the main index or repo home.
	ri, err := os.Create(filepath.Join(out, "index.html"))

	defer ri.Close()

	if err != nil {
		log.Fatalf("unable to create repo index: %v", err)
	}

	rt := template.Must(template.New("home").Parse(rTmpl))
	rd := struct {
		Branches []string
		Link     string
		Title    string
	}{
		Branches: branches,
		Link:     opt.URL,
		Title:    opt.Project,
	}

	if err := rt.Execute(ri, rd); err != nil {
		log.Fatalf("unable to fill in repo template: %v", err)
	}
}

func (r *repo) init(f bool) error {
	dirs := []string{"branch", "commit", "object"}

	for _, dir := range dirs {
		d := filepath.Join(r.base, dir)

		// Clear existing dirs if force true.
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

func (r *repo) save(target string) error {
	_, err := os.Stat(r.path)

	if err := exec.Command("git", "clone", target, r.path).Run(); err != nil {
		return err
	}

	// NOTE: Should this be in a separate method.
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

func (r *repo) findBranches(bf manyflag) ([]string, error) {
	cmd := exec.Command("git", "branch", "-a")
	cmd.Dir = r.path

	out, err := cmd.Output()

	if err != nil {
		return nil, err
	}

	var result []string
	var branch = make(map[string]bool)

	scanner := bufio.NewScanner(bytes.NewReader(out))

	for scanner.Scan() {
		t := strings.TrimSpace(scanner.Text())
		_, f := filepath.Split(t)

		branch[f] = !strings.Contains(f, "HEAD")
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	// Filter to match options, but return all if no branch flags given.
	if len(bf) > 0 {
		for k := range branch {
			branch[k] = contains(bf, k)
		}
	}

	// Transfer desired branch names to resulting slice.
	for k, v := range branch {
		if v {
			result = append(result, k)
		}
	}

	return result, nil
}

// TODO: implement!
func doTheRealWork() {
	/*
	   b=0
	   for branch in $BRANCHES
	   do
	     let ++b

	     cd "$TARGET/repository"

	     COMMITS=$(mktemp)
	     git rev-list --graph "origin/$branch" > $COMMITS

	     # Count the number of commits on this branch to improve reporting.
	     ccount=$(egrep '[0-9a-f]' < $COMMITS | wc -l)

	     progress "Branch $branch ($b/$bcount): processing ($ccount commits)."

	     BRANCH_INDEX="$TARGET/branches/$branch.html"

	     c=0
	     while read -r commitline
	     do
	       # See http://www.itnewb.com/unicode
	       graph=$(echo "$commitline" \
	               | sed 's/ [0-9a-f]*$//; s/|/\&#x2503;/g; s/[*]/\&#x25CF;/g;
	                      s/[\]/\&#x2B0A;/g; s/\//\&#x2B0B;/g;')
	*/
	//    commit=$(echo "$commitline" | sed 's/^[^0-9a-f]*//')
	/*
	     if test x"$commit" = x
	     then
	       # This is just a bit of graph.  Add it to the branch's
	       # index.html and then go to the next commit.
	       echo "<tr><td valign=\"middle\"><pre>$graph</pre></td><td></td><td></td><td></td></tr>" \
	   >> "$BRANCH_INDEX"
	       continue
	     fi

	     let ++c
	     progress "Commit $commit ($c/$ccount): processing."

	     # Extract metadata about this commit.
	     metadata=$(git log -n 1 --pretty=raw $commit \
	         | sed 's#<#\&lt;#g; s#>#\&gt;#g; ')
	     parent=$(echo "$metadata" \
	   | gawk '/^parent / { $1=""; sub (" ", ""); print $0 }')
	     author=$(echo "$metadata" \
	   | gawk '/^author / { NF=NF-2; $1=""; sub(" ", ""); print $0 }')
	     date=$(echo "$metadata" | gawk '/^author / { print $(NF=NF-1); }')
	     date=$(date -u -d "1970-01-01 $date sec")
	     log=$(echo "$metadata" | gawk '/^    / { if (!done) print $0; done=1; }')
	     loglong=$(echo "$metadata" | gawk '/^    / { print $0; }')

	     if test "$c" = "1"
	     then
	       # This commit is the current head of the branch.  Update the
	       # branch's link, but don't use ln -sf: because the symlink is to
	       # a directory, the symlink won't be replaced; instead, the new
	       # link will be created in the existing symlink's target
	       # directory:
	       #
	       #   $ mkdir foo
	       #   $ ln -s foo bar
	       #   $ ln -s baz bar
	       #   $ ls -ld bar bar/baz
	       #   lrwxrwxrwx 1 neal neal 3 Aug  3 09:14 bar -> foo
	       #   lrwxrwxrwx 1 neal neal 3 Aug  3 09:14 bar/baz -> baz
	       rm -f "$TARGET/branches/$branch"
	       ln -s "../commits/$commit" "$TARGET/branches/$branch"

	       # Update the project's index.html and the branch's index.html.
	       echo "<li><a href=\"branches/$branch.html\">$branch</a>: " \
	         "<b>$log</b> $author <i>$date</i>" >> "$INDEX"

	       {
	         html_header "Branch: $branch" ".."
	   echo "<p><a href=\"$branch/index.html\">HEAD</a>"
	         echo "<p><table>"
	       } > "$BRANCH_INDEX"
	     fi

	     # Add this commit to the branch's index.html.
	     echo "<tr><td valign=\"middle\"><pre>$graph</pre></td><td><a href=\"../commits/$commit/index.html\">$log</a></td><td>$author</td><td><i>$date</i></td></tr>" \
	   >> "$BRANCH_INDEX"


	     # Commits don't change.  If the directory already exists, it is up
	     # to date and we can save some work.
	     COMMIT_BASE="$TARGET/commits/$commit"
	     if test -e "$COMMIT_BASE"
	     then
	       progress "Commit $commit ($c/$ccount): already processed."
	       continue
	     fi

	     mkdir "$COMMIT_BASE"

	     # Get the list of files in this commit.
	     FILES=$(mktemp)
	     git ls-tree -r "$commit" > "$FILES"

	     # Create the commit's index.html: the metadata, a summary of the changes
	     # and a list of all the files.
	     COMMIT_INDEX="$COMMIT_BASE/index.html"
	     {
	       html_header "Commit: $commit" "../.."

	       # The metadata.
	       echo "<h2>Branch: <a href=\"../../branches/$branch.html\">$branch</a></h2>" \
	   "<p>Author: $author" \
	   "<br>Date: $date" \
	         "<br>Commit: $commit"
	       for p in $parent
	       do
	         echo "<br>Parent: <a href=\"../../commits/$p/index.html\">$p</a>" \
	   " (<a href=\"../../commits/$commit/diff-to-$p.html\">diff to parent</a>)"
	       done
	       echo "<br>Log message:" \
	   "<p><pre>$loglong</pre>"
	       for p in $parent
	       do
	   echo "<br>Diff Stat to $p:" \
	     "<blockquote><pre>"
	         git diff --stat $p..$commit \
	           | gawk \
	               '{ if (last_line) print last_line;
	                  last_line_raw=$0;
	                  $1=sprintf("<a href=\"%s.raw.html\">%s</a>" \
	                             " (<a href=\"../../commits/'"$p"'/%s.raw.html\">old</a>)" \
	                             "%*s" \
	                             "(<a href=\"diff-to-'"$p"'.html#%s\">diff</a>)",
	                             $1, $1, $1, 60 - length ($1), " ", $1);
	                     last_line=$0; }
	                   END { print last_line_raw; }'
	         echo "</pre></blockquote>"
	       done
	       echo "<p>Files:" \
	         "<ul>"

	       # The list of files as a hierarchy.  Sort them so that within a
	       # directory, files preceed sub-directories
	       sed 's/\([^ \t]\+[ \t]\)\{3\}//;
	*/
	//                 s#^#/#; s#/\([^/]*/\)#/1\1#; s#/\([^/]*\)$#/0\1#;' \
	/*
	         < "$FILES" \
	   | sort | sed 's#/[01]#/#g; s#^/##' \
	   | gawk '
	          function spaces(l) {
	            for (space = 1; space <= l; space ++) { printf ("  "); }
	          }
	          function max(a, b) { if (a > b) { return a; } return b; }
	          function min(a, b) { if (a < b) { return a; } return b; }
	          function join(array, sep, i, s) {
	            s="";
	            for (i in array) {
	              if (s == "")
	                s = array[i];
	              else
	                s = s sep array[i];
	            }
	            return s;
	          }
	          BEGIN {
	            current_components[1] = "";
	            delete current_components[1];
	          }
	          {
	            file=$0;
	            split(file, components, "/")
	            # Remove the file.  Keep the directories.
	            file=components[length(components)]
	            delete components[length(components)];

	            # See if a path component changed.
	            for (i = 1;
	                 i <= min(length(components), length(current_components));
	                 i ++)
	            {
	              if (current_components[i] != components[i])
	                # It did.
	                break
	            }
	            # i-1 is the last common component.  The rest from the
	            # current_component stack.
	            last=length(current_components);
	            for (j = last; j >= i; j --)
	            {
	              spaces(j);
	              printf ("</ul> <!-- %s -->\n", current_components[j]);
	              delete current_components[j];
	            }

	            # If there are new path components push them on the
	            # current_component stack.
	            for (; i <= length(components); i ++)
	            {
	                current_components[i] = components[i];
	                spaces(i);
	                printf("<li><a name=\"files:%s\">%s</a>\n",
	                       join(current_components, "/"), components[i]);
	                spaces(i);
	                printf("<ul>\n");
	            }

	            spaces(length(current_components))
	            printf ("<li><a name=\"files:%s\" href=\"%s.raw.html\">%s</a>\n",
	                    $0, $0, file);
	            printf ("  (<a href=\"%s\">raw</a>)\n", $0, file);
	          }
	          END {
	            for (i = length(current_components); j >= 1; j --)
	            {
	              spaces(j);
	              printf ("</ul> <!-- %s -->\n", current_components[j]);
	              delete current_components[j];
	            }
	          }'

	     echo "</ul>"
	     html_footer
	   } > "$COMMIT_INDEX"

	   # Create the commit's diff-to-parent.html file.
	   for p in $parent
	   do
	     {
	*/
	//        html_header "diff $(echo $commit | sed 's/^\(.\{8\}\).*/\1/') $(echo $p | sed 's/^\(.\{8\}\).*/\1/')" "../.."
	/*
	           echo "<h2>Branch: <a href=\"../../branches/$branch.html\">$branch</a></h2>" \
	             "<h3>Commit: <a href=\"index.html\">$commit</a></h3>" \
	       "<p>Author: $author" \
	       "<br>Date: $date" \
	       "<br>Parent: <a href=\"../$p/index.html\">$p</a>" \
	       "<br>Log message:" \
	       "<p><pre>$loglong</pre>" \
	       "<p>" \
	             "<pre>"
	           git diff -p $p..$commit \
	             | sed 's#<#\&lt;#g; s#>#\&gt;#g;
	                    s#^\(diff --git a/\)\([^ ]\+\)#\1<a name="\2">\2</a>#;
	                    s#^\(\(---\|+++\|index\|diff\|deleted\|new\) .\+\)$#<b>\1</b>#;
	                    s#^\(@@ .\+\)$#<font color=\"blue\">\1</font>#;
	                    s#^\(-.*\)$#<font color=\"red\">\1</font>#;
	                    s#^\(+.*\)$#<font color=\"green\">\1</font>#;' \
	             | gawk '{ ++line; printf("%5d: %s\n", line, $0); }'
	           echo "</pre>"
	           html_footer
	         } > "$COMMIT_BASE/diff-to-$p.html"
	       done


	       # For each file in the commit, ensure the object exists.
	       while read -r line
	       do
	         file_base=$(echo "$line" | gawk '{ print $4 }')
	         file="$TARGET/commits/$commit/$file_base"
	         sha=$(echo "$line" | gawk '{ print $3 }')

	         object_dir="$TARGET/objects/"$(echo "$sha" \
	       | sed 's#^\([a-f0-9]\{2\}\).*#\1#')
	         object="$object_dir/$sha"

	         if test ! -e "$object"
	         then
	           # File does not yet exists in the object repository.
	           # Create it.
	     if test ! -d "$object_dir"
	     then
	       mkdir "$object_dir"
	     fi

	           # The object's file should not be commit or branch specific:
	           # the same html is shared among all files with the same
	           # content.
	           {
	             html_header "$sha"
	             echo "<pre>"
	             git show "$sha" \
	               | sed 's#<#\&lt;#g; s#>#\&gt;#g; ' \
	               | gawk '{ ++line; printf("%6d: %s\n", line, $0); }'
	             echo "</pre>"
	             html_footer
	           } > "$object"
	         fi

	         # Create a hard link to the formatted file in the object repository.
	         mkdir -p $(dirname "$file")
	         ln "$object" "$file.raw.html"

	         # Create a hard link to the raw file.
	         raw_filename="raw/$(echo "$sha" | sed 's/^\(..\)/\1\//')"
	         if ! test -e "$raw_filename"
	         then
	       mkdir -p "$(dirname "$raw_filename")"
	       git cat-file blob "$sha" > $raw_filename
	         fi
	         ln "$raw_filename" "$file"
	       done <"$FILES"
	       rm -f "$FILES"
	     done <$COMMITS
	     rm -f $COMMITS

	     {
	       echo "</table>"
	       html_footer
	     } >> "$BRANCH_INDEX"
	   done
	*/
}
