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

