package reviewview

import (
	"sort"
	"strings"
)

// treeRow is a line of the file tree: a directory or a file.
type treeRow struct {
	depth int
	name  string
	file  int // index in Model.files, -1 for a directory
}

type treeNode struct {
	name     string
	dirs     map[string]*treeNode
	files    []treeFile
	fileName string
}

type treeFile struct {
	name string
	idx  int
}

func newTreeNode(name string) *treeNode {
	return &treeNode{name: name, dirs: map[string]*treeNode{}}
}

// buildTree lays paths out like GitHub's file tree: nested directories,
// chains of single directories merged into one row ("java/com/android"),
// directories before files, both sorted by name.
func buildTree(paths []string) []treeRow {
	root := newTreeNode("")
	for i, p := range paths {
		parts := strings.Split(p, "/")
		n := root
		for _, d := range parts[:len(parts)-1] {
			child, ok := n.dirs[d]
			if !ok {
				child = newTreeNode(d)
				n.dirs[d] = child
			}
			n = child
		}
		n.files = append(n.files, treeFile{name: parts[len(parts)-1], idx: i})
	}

	var rows []treeRow
	var walk func(n *treeNode, depth int)
	walk = func(n *treeNode, depth int) {
		names := make([]string, 0, len(n.dirs))
		for name := range n.dirs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			d := n.dirs[name]
			label := d.name
			// Merge directories holding a single directory and no file.
			for len(d.files) == 0 && len(d.dirs) == 1 {
				for _, only := range d.dirs {
					label += "/" + only.name
					d = only
				}
			}
			rows = append(rows, treeRow{depth: depth, name: label, file: -1})
			walk(d, depth+1)
		}
		files := append([]treeFile(nil), n.files...)
		sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
		for _, f := range files {
			rows = append(rows, treeRow{depth: depth, name: f.name, file: f.idx})
		}
	}
	walk(root, 0)
	return rows
}

// orderFilesLikeTree sorts the files in the order of the tree, so that
// [ and ] go through them as they are shown, and builds the tree.
func (m *Model) orderFilesLikeTree() {
	paths := make([]string, len(m.files))
	for i, f := range m.files {
		paths[i] = f.Filename
	}
	rows := buildTree(paths)
	ordered := make([]fileDiff, 0, len(m.files))
	for i, r := range rows {
		if r.file >= 0 {
			ordered = append(ordered, m.files[r.file])
			rows[i].file = len(ordered) - 1
		}
	}
	m.files = ordered
	m.tree = rows
}
