package treemultiselect

import (
	"testing"
)

func TestItemTree(t *testing.T) {
	tms := TreeMultiSelect{separator: "/"}

	t.Run("Insert a tree", func(t *testing.T) {
		paths := []string{
			"foo/bar/1.txt",
			"foo/bar/2.txt",
			"foo/bar/3.txt",
			"foo/bar2/baz",
			"foo2/bar3/baz",
			"foo2/bar4/baz",
		}

		tms.initializeTree(paths)
		root := tms.root
		nodesMap := tms.nodeMap

		if len(root.Children) != 2 {
			t.Errorf("root should have 2 children, got %d", len(root.Children))
		}
		if len(root.Children["foo"].Children) != 2 {
			t.Errorf("root.foo should have 2 children, got %d", len(root.Children["foo"].Children))
		}
		if len(root.Children["foo"].Children["bar"].Children) != 3 {
			t.Errorf("root.foo.bar should have 3 children, got %d", len(root.Children["foo"].Children["bar"].Children))
		}

		if len(nodesMap) != 13 {
			t.Errorf("nodesMap should have 13 elements, got %d", len(nodesMap))
		}
	})
}
