package main

import (
	"crypto/sha1"
	_ "embed"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/go-git/go-git/v5/storage/memory"
)

// CONFIG_FILE=".ht_git2html"
const configFile = ".config"

/*
show_progress=1
force_rebuild=0
*/
const showProgress = true
const forceRebuild = false

// TODO: add log.Debug
/*
 progress()
 {
   if test x"$show_progress" = x1
   then
     echo "$@"
   fi
 }
*/

//go:embed config.tmpl
var cTmpl string

//go:embed repo.html.tmpl
var rTmpl string

//go:embed branch.html.tmpl
var bTmpl string

//go:embed index.html.tmpl
var iTmpl string

type options struct {
	project  string
	repo     string
	link     string
	branches string
	quiet    bool
	force    bool
}

func init() {
	// Override default usage output.
	flag.Usage = func() {
		// Print usage example ahead of lisiting default options.
		fmt.Fprintln(flag.CommandLine.Output(), "usage:", os.Args[0], "[<options>] <path>")
		flag.PrintDefaults()
	}
}

/*
usage()
	{
	  echo "Usage $0 [-prlbq] TARGET"
	  echo "Generate static HTML pages in TARGET for the specified git repository."
	  echo
	  echo "  -p  Project's name"
	  echo "  -r  Repository to clone from."
	  echo "  -l  Public repository link, e.g., 'http://host.org/project.git'"
	  echo "  -b  List of branches to process (default: all)."
	  echo "  -q  Be quiet."
	  echo "  -f  Force rebuilding of all pages."
	  exit $1
	}
*/
func main() {
	/*
	   while getopts ":p:r:l:b:qf" opt
	   do
	     case $opt in
	       p)
	         PROJECT=$OPTARG
	         ;;
	       r)
	         # Directory containing the repository.
	         REPOSITORY=$OPTARG
	         ;;
	       l)
	         PUBLIC_REPOSITORY=$OPTARG
	         ;;
	       b)
	         BRANCHES=$OPTARG
	         ;;
	       q)
	         show_progress=0
	         ;;
	       f)
	         force_rebuild=1
	         ;;
	       \?)
	         echo "Invalid option: -$OPTARG" >&2
	         usage
	         ;;
	     esac
	   done
	   shift $(($OPTIND - 1))
	*/
	opts := &options{}

	flag.StringVar(&opts.project, "p", "My project", "Project's name")
	flag.StringVar(&opts.repo, "r", "", "Repository to clone from.")
	flag.StringVar(&opts.link, "l", "http://host.org/project.git", "Public repository link, e.g., 'http://host.org/project.git'")
	flag.StringVar(&opts.branches, "b", "all", "List of branches (default: all)")
	flag.BoolVar(&opts.quiet, "q", false, "Be quiet.")
	flag.BoolVar(&opts.force, "f", false, "Force rebuilding of all pages.")
	flag.Parse()

	// Collect flags provided. Note these need to come before
	// the target directory argument.
	flagset := make(map[string]bool)

	flag.Visit(func(f *flag.Flag) {
		flagset[f.Name] = true
	})

	// TODO: Log these one by one unless quiet.
	// log.Printf("+%v", opts)

	// The repo flag is required, print usage and quit if none given or
	// unless a single target directory is provided.
	if !flagset["r"] || flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}

	target := flag.Arg(0)

	// Make sure `target` is an absolute path.
	if ok := filepath.IsAbs(target); !ok {
		cwd, err := os.Getwd()

		if err != nil {
			log.Fatalf("jimmy: unable to get current working directory %v", err)
		}

		target = filepath.Join(cwd, target)
	}

	// Make sure `target` exists.
	if err := os.MkdirAll(target, 0750); err != nil {
		log.Fatalf("jimmy: unable to create target directory: %v", err)
	}

	// Read the configuration file.
	/*
	   if test -e "$TARGET/$CONFIG_FILE"
	   then
	     . "$TARGET/$CONFIG_FILE"
	   fi
	*/
	// TODO: Read config file
	writeConfigFile(target, opts)
	createDirectories(target, opts.force)

	// NOTE: I believe this check is too limiting and we should
	// allow for cloning no local repos as well. Once the repo
	// has been copied or downloaded, we should then check if it
	// contains a hidden `.git` folder to verify true repo status?
	/*
	   if test ! -d "$REPOSITORY"
	   then
	     echo "Repository \"$REPOSITORY\" does not exists.  Misconfiguration likely."
	     exit 1
	   fi
	*/
	setUpRepo(target, opts)
	setGitConfig()

	cleanBranches := cleanUpBranches(opts.branches)

	fetchBranches(cleanBranches)
	writeIndex()
	doTheRealWork()
	writeIndexFooter()
}

func writeConfigFile(target string, opts *options) {
	/*
	   # The output version
	   CURRENT_TEMPLATE="$(sha1sum "$0")"
	   if test "x$CURRENT_TEMPLATE" != "x$TEMPLATE"
	   then
	     progress "Rebuilding all pages as output template changed."
	     force_rebuild=1
	   fi
	   TEMPLATE="$CURRENT_TEMPLATE"
	*/
	configTmpl := template.Must(template.New("default").Parse(cTmpl))

	// TODO: Check file permissions are set to 0666.
	// TODO: Read file if it exists.
	outFile, err := os.Create(filepath.Join(target, configFile))

	if err != nil {
		log.Fatalf("jimmy: unable to create config file: %v", err)
	}

	h := sha1.New()

	// (spike): why did we do this step?
	if _, err := io.Copy(h, outFile); err != nil {
		log.Fatal(err)
	}

	/*
	   {
	     save()
	     {
	       # Prefer environment variables and arguments to the configuration file.
	       echo "$1=\"\${$1:-\"$2\"}\""
	     }
	     save "PROJECT" "$PROJECT"
	     save "REPOSITORY" "$REPOSITORY"
	     save "PUBLIC_REPOSITORY" "$PUBLIC_REPOSITORY"
	     save "TARGET" "$TARGET"
	     save "BRANCHES" "$BRANCHES"
	     save "TEMPLATE" "$TEMPLATE"
	   } > "$TARGET/$CONFIG_FILE"
	*/
	configTmpl.Execute(outFile, struct {
		Project          string
		Repository       string
		PublicRepository string
		Target           string
		Branches         string
		// SHA1SUM
		Template string
	}{
		Project:          opts.project,
		Repository:       opts.repo,
		PublicRepository: opts.link,
		Target:           target,
		Branches:         opts.branches,
		Template:         hex.EncodeToString(h.Sum(nil)),
	})
}

func createDirectories(target string, force bool) {
	//# Ensure that some directories we need exist.
	/*
	   if test x"$force_rebuild" = x1
	   then
	     rm -rf "$TARGET/objects" "$TARGET/commits"
	   fi

	   if test ! -d "$TARGET/objects"
	   then
	     mkdir "$TARGET/objects"
	   fi

	   if test ! -e "$TARGET/commits"
	   then
	     mkdir "$TARGET/commits"
	   fi

	   if test ! -e "$TARGET/branches"
	   then
	     mkdir "$TARGET/branches"
	   fi
	*/

	// Repository
	dirs := []string{"branches", "commits", "objects"}

	for _, dir := range dirs {
		d := filepath.Join(target, dir)

		// Clear existing dirs if force true.
		if force && dir != "branches" {
			if err := os.RemoveAll(d); err != nil {
				log.Printf("jimmy: unable to remove directory: %v", err)
			}
		}

		if err := os.MkdirAll(d, os.ModePerm); err != nil {
			log.Printf("jimmy: unable to create directory: %v", err)
		}
	}
}

func setUpRepo(target string, opts *options) {
	mfs := memfs.New()
	// Clones the given repository in memory, creating the remote, the local
	// branches and fetching the objects, exactly as:
	r, err := git.Clone(memory.NewStorage(), mfs, &git.CloneOptions{
		URL: opts.repo,
	})

	check(err)

	refs, _ := r.References()
	refs.ForEach(func(ref *plumbing.Reference) error {
		bc, err := r.Branch("refs/head/experimental")

		if err != nil {
			return err
		}

		log.Printf("reference: %v %v", ref.Type(), ref.Name())
		log.Printf("branch: %v", bc.Name)

		return nil
	})

	branches, err := r.Branches()

	check(err)

	// ... retrieves the branch pointed by HEAD
	// ref, err := r.Head()
	//
	// check(err)
	//
	// cIter, err := r.Log(&git.LogOptions{From: ref.Hash()})
	//
	// check(err)

	// err = cIter.ForEach(func(c *object.Commit) error {
	// 	// log.Print(c)
	//
	// 	return nil
	// })

	branches.ForEach(func(b *plumbing.Reference) error {
		log.Printf("branch: %v", b)

		return nil
	})

	check(err)

	var pathError *fs.PathError
	repoPath := filepath.Join(target, "repository")

	_, err = os.Stat(repoPath)

	if errors.As(err, &pathError) {
		localRepo, err := git.PlainClone(repoPath, false, &git.CloneOptions{
			URL:          opts.repo,
			SingleBranch: false,
			NoCheckout:   true,
			// NOTE: This will screw things up if piping output to a file.
			// Progress: os.Stdout,
		})

		commitObjects, err := localRepo.CommitObjects()

		if err != nil {
			log.Printf("%v", err)
		}

		var commitList []*object.Commit

		commitObjects.ForEach(func(c *object.Commit) error {
			commitList = append(commitList, c)

			return nil
		})

		// it := template.Must(template.New("default").Parse(iTmpl))
		//
		// cdata := struct {
		// 	List  []*object.Commit
		// 	Title string
		// }{
		// 	List:  commitList,
		// 	Title: opts.project,
		// }
		//
		// if err := it.Execute(os.Stdout, cdata); err != nil {
		// 	log.Fatalf("jimmy: unable to fill index template: %v", err)
		// }

		localBranches, err := localRepo.Branches()

		if err != nil {
			log.Printf("%v", err)
		}

		var branchList []*plumbing.Reference

		localBranches.ForEach(func(b *plumbing.Reference) error {
			branchList = append(branchList, b)

			return nil
		})

		rt := template.Must(template.New("default").Parse(rTmpl))

		rconf, err := localRepo.Config()

		check(err)

		for _, b := range rconf.Branches {
			log.Print(b.Name)
		}

		log.Printf("config/branches: %v", rconf.Branches)
		log.Printf("config/remotes: %v", rconf.Remotes)

		bdata := struct {
			Description string
			Link        string
			List        []*plumbing.Reference
			Title       string
		}{
			Description: "",
			Link:        opts.link,
			List:        branchList,
			Title:       opts.project,
		}

		if err := rt.Execute(os.Stdout, bdata); err != nil {
			log.Fatalf("jimmy: unable to fill home template: %v", err)
		}

		// branch, err := localBranches.Next()
		//
		// log.Printf("branch: %v %s", branch.Name(), branch.String())
		//
		// if err != nil {
		// 	log.Printf("jimmy: failed to list branches: %v", err)
		// }
		//
		// ref := plumbing.NewHashReference(branch.Name(), branch.Hash())
		//
		// if err != nil {
		// 	log.Printf("jimmy: failed to create ref: %v", err)
		// }

		// workTree, err := localRepo.Worktree()
		//
		// if err != nil {
		// 	log.Printf("jimmy: failed to open worktree: %v", err)
		// }
		//
		// err = workTree.Checkout(&git.CheckoutOptions{
		// 	Hash: ref.Hash(),
		// })
		//
		// if err != nil {
		// 	log.Printf("jimmy: failed to checkout detached HEAD: %v", err)
		// }
		//
		// err = localRepo.Storer.RemoveReference(ref.Name())
		//
		// if err != nil {
		// 	log.Printf("jimmy: failed to delete branch: %v", err)
		// }
	}
}

// TODO: implement!
// NOTE: This may not be required, there is no merge calls anywhere.
func setGitConfig() {
	/*
	   # git merge fails if there are not set.  Fake them.
	   git config user.email "git2html@git2html"
	   git config user.name "git2html"
	*/
}

// TODO: implement!
func cleanUpBranches(branches string) []string {
	/*
	   if test x"$BRANCHES" = x
	   then
	     # Strip the start of lines of the form 'origin/HEAD -> origin/master'
	     BRANCHES=$(git branch --no-color -r \
	                  | sed 's#.*->##; s#^ *origin/##;')
	   fi

	   first=""
	   # Ignore 'origin/HEAD -> origin/master'
	   for branch in ${BRANCHES:-$(git branch --no-color -r \
	                                 | sed 's#.*->.*##;
	                                        s#^ *origin/##;
	                                        s#^ *HEAD *$##;')}
	   do
	     first="$branch"
	     break
	   done

	   # Due to branch aliases (a la origin/HEAD), a branch might be listed
	   # multiple times.  Eliminate this possibility.
	   BRANCHES=$(for branch in $BRANCHES
	     do
	       echo "$branch"
	     done | sort | uniq)
	*/
	return []string{}
}

// TODO: implement!
func fetchBranches(branches []string) {
	/*
	   	   for branch in $BRANCHES
	   	   do
	   	     # Suppress already up to date status messages, but don't use grep -v
	   	     # as that returns 1 if there is no output and causes the script to
	   	     # abort.
	   	     git fetch --force origin "refs/heads/${branch}:refs/origin/${branch}" \
	   	         | gawk '/^Already up-to-date[.]$/ { skip=1; }
	   	                 { if (! skip) print; skip=0 }'
	   	   done
	   	   git checkout "origin/$first"
	      }

	   	   # For each branch and each commit create and extract an archive of the form
	   	   #   $TARGET/commits/$commit
	   	   #
	   	   # and a link:
	   	   #
	   	   #   $TARGET/branches/$commit -> $TARGET/commits/$commit

	   	   # Count the number of branch we want to process to improve reporting.
	   	   bcount=0
	   	   for branch in $BRANCHES
	   	   do
	   	     let ++bcount
	   	   done
	*/
}

// TODO: implement!
func writeIndex() {
	/*
	   INDEX="$TARGET/index.html"

	   {
	     html_header

	     if test -e "$REPOSITORY/description"
	     then
	       echo "<h2>Description</h2>"
	       cat "$REPOSITORY/description"
	     fi

	     echo "<h2>Repository</h2>"
	     if test x"$PUBLIC_REPOSITORY" != x
	     then
	       echo  "Clone this repository using:" \
	         "<pre>" \
	         " git clone $PUBLIC_REPOSITORY" \
	         "</pre>"
	     fi

	     echo "<h2>Branches</h2>" \
	       "<ul>"
	   } > "$INDEX"

	*/
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

// TODO: implement!
func writeIndexFooter() {
	/*
	   {
	     echo "</ul>"
	     html_footer
	   } >> "$INDEX"
	*/
}

// TODO: implement!
func htmlHeader() {
	/*
	   html_header()
	   {
	     title="$1"
	     top_level="$2"

	     if test x"$PROJECT" != x -a x"$title" != x
	     then
	       # Title is not the empty string.  Prefix it with ": "
	       title=": $title"
	     fi

	     echo "<html><head><title>$PROJECT$title</title></head>" \
	       "<body>" \
	       "<h1><a href=\"$top_level/index.html\">$PROJECT</a>$title</h1>"
	   }
	*/
}

func htmlFooter() {
	/*
	   html_footer()
	   {
	     echo "<hr>" \
	       "Generated by" \
	       "<a href=\"http://hssl.cs.jhu.edu/~neal/git2html\">git2html</a>."
	   }
	*/
}

func check(err error) {
	if err != nil {
		log.Fatalf("jimmy: %v", err)
	}
}
