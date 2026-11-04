package tgminiapp

import (
	"path"
	"sort"
	"strings"

	"tg-podcastotron/service"
)

// fileNode is a folder or a file of a torrent in the file selection tree.
type fileNode struct {
	Name   string // a folder with only one subfolder has the joined name, e.g. "a/b"
	Path   string // the variant ID of a file
	IsFile bool
	Size   int64 // of the file, or of all files in the folder
	Files  int   // number of files in the folder
	// TopExt is the most frequent file extension in the folder, and TopExtCount is
	// the number of files with it. The folder has an "All .<TopExt>" button.
	TopExt      string
	TopExtCount int
	Children    []*fileNode
}

// ShowActions tells if the folder gets the All and None buttons. A folder with
// one file does not need them: its checkbox does the same.
func (n *fileNode) ShowActions() bool { return n.Files >= 2 }

// ShowExt tells if the folder gets the "All .<TopExt>" button. It does not get it
// if all its files have that extension, because then "All" does the same.
func (n *fileNode) ShowExt() bool { return n.TopExt != "" && n.TopExtCount < n.Files }

// buildFileTree makes a tree from the variant IDs (file paths). Folders come before
// files, and names sort in natural order ("2" before "10").
func buildFileTree(variants []service.Variant) *fileNode {
	root := &fileNode{}
	folders := map[string]*fileNode{"": root}

	var folder func(p string) *fileNode
	folder = func(p string) *fileNode {
		if f, ok := folders[p]; ok {
			return f
		}
		parent := folder(parentDir(p))
		f := &fileNode{Name: path.Base(p)}
		parent.Children = append(parent.Children, f)
		folders[p] = f
		return f
	}

	for _, v := range variants {
		p := strings.Trim(v.ID, "/")
		var size int64
		if v.LenBytes != nil {
			size = *v.LenBytes
		}
		parent := folder(parentDir(p))
		parent.Children = append(parent.Children, &fileNode{
			Name:   path.Base(p),
			Path:   v.ID,
			IsFile: true,
			Size:   size,
		})
	}

	finishTree(root)
	// A tree with one top folder shows the files of that folder at the top.
	for len(root.Children) == 1 && !root.Children[0].IsFile {
		root = root.Children[0]
	}
	return root
}

func parentDir(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

// finishTree joins folders that have only one subfolder, counts files, sizes and
// extensions, and sorts the children. It returns the number of files of each extension.
func finishTree(n *fileNode) map[string]int {
	exts := map[string]int{}
	n.Files, n.Size = 0, 0
	for _, c := range n.Children {
		if c.IsFile {
			n.Files++
			if ext := fileExt(c.Path); ext != "" {
				exts[ext]++
			}
		} else {
			for len(c.Children) == 1 && !c.Children[0].IsFile {
				only := c.Children[0]
				c.Name = c.Name + "/" + only.Name
				c.Children = only.Children
			}
			for ext, count := range finishTree(c) {
				exts[ext] += count
			}
			n.Files += c.Files
		}
		n.Size += c.Size
	}
	for ext, count := range exts {
		if count > n.TopExtCount || (count == n.TopExtCount && ext < n.TopExt) {
			n.TopExt, n.TopExtCount = ext, count
		}
	}
	sort.SliceStable(n.Children, func(i, j int) bool {
		a, b := n.Children[i], n.Children[j]
		if a.IsFile != b.IsFile {
			return !a.IsFile
		}
		return naturalLess(a.Name, b.Name)
	})
	return exts
}

func fileExt(p string) string {
	return strings.ToLower(strings.TrimPrefix(path.Ext(p), "."))
}

// naturalLess compares strings so that numbers in them sort by value: "2" < "10".
func naturalLess(a, b string) bool {
	a, b = strings.ToLower(a), strings.ToLower(b)
	for a != "" && b != "" {
		an, arest := leadingNumber(a)
		bn, brest := leadingNumber(b)
		if an != "" && bn != "" {
			if an != bn {
				// Compare numbers without leading zeros: first by length, then by digits.
				at, bt := strings.TrimLeft(an, "0"), strings.TrimLeft(bn, "0")
				if len(at) != len(bt) {
					return len(at) < len(bt)
				}
				if at != bt {
					return at < bt
				}
			}
			a, b = arest, brest
			continue
		}
		if a[0] != b[0] {
			return a[0] < b[0]
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func leadingNumber(s string) (num, rest string) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i], s[i:]
}
