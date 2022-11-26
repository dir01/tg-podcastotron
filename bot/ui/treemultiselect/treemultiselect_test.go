package treemultiselect

import (
	"testing"
)

func TestNodeToPath(t *testing.T) {
	leaf := &TreeNode{Value: "1.txt"}
	b := &TreeNode{Value: "b"}
	a2 := &TreeNode{Value: "a"}
	c := &TreeNode{Value: "c"}
	a1 := &TreeNode{Value: "a"}
	root := &TreeNode{}

	leaf.Parent = b
	b.Parent = a2
	a2.Parent = c
	c.Parent = a1
	a1.Parent = root

	path := nodeToPath(leaf)

	if path != "a/c/a/b/1.txt" {
		t.Errorf("path should be a/c/a/b/1.txt, got %s", path)
	}
}
