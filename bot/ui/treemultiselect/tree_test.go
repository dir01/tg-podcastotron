package treemultiselect

import (
	"testing"
)

func TestItemTree(t *testing.T) {
	t.Run("Insert preserves leaf id", func(t *testing.T) {
		items := []Item{
			{Text: "foo/bar/baz", ID: 666},
		}
		root, _ := createTree(items, "/")
		if id := root.Children["foo"].Children["bar"].Children["baz"].Item.ID; id != 666 {
			t.Errorf("tree is supposed to preserve ids of leaves. expected %d, got %d", 666, id)
		}
	})

	t.Run("Insert a tree", func(t *testing.T) {
		paths := []string{
			"foo/bar/1.txt",
			"foo/bar/2.txt",
			"foo/bar/3.txt",
			"foo/bar2/baz",
			"foo2/bar3/baz",
			"foo2/bar4/baz",
		}
		items := make([]Item, len(paths))
		for i, path := range paths {
			items[i] = Item{Text: path, ID: i}
		}

		root, nodesMap := createTree(items, "/")

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

		if nodesMap[0].Item.Text != "1.txt" {
			t.Errorf("nodesMap[0] should have text '1.txt', got %s", nodesMap[0].Item.Text)
		}
		if nodesMap[1].Item.Text != "2.txt" {
			t.Errorf("nodesMap[1] should have text '2.txt', got %s", nodesMap[1].Item.Text)
		}
		if nodesMap[2].Item.Text != "3.txt" {
			t.Errorf("nodesMap[2] should have text '3.txt', got %s", nodesMap[2].Item.Text)
		}
		if nodesMap[3].Item.Text != "baz" {
			t.Errorf("nodesMap[3] should have text 'baz', got %s", nodesMap[3].Item.Text)
		}
		if nodesMap[4].Item.Text != "baz" {
			t.Errorf("nodesMap[4] should have text 'baz', got %s", nodesMap[4].Item.Text)
		}
		if nodesMap[5].Item.Text != "baz" {
			t.Errorf("nodesMap[5] should have text 'baz', got %s", nodesMap[5].Item.Text)
		}
	})

	t.Run("Insert a tree - 2", func(t *testing.T) {
		paths := []string{
			"Users/vinno/Term Papers/asdf",
			"Users/bobby/Term Papers/asdf/1.mp4",
			"Users/bobby/Term Papers/asdf/2.mp4",
			"Users/bobby/Term Papers/asdf/3.mp4",
			"Users/bobby/Term Papers/1",
			"Users/bobby/Chemistry/1",
			"Users/bobby/Algebra/1",
			"Users/bobby/Probability Theory/1",
			"Users/bobby/Philosophy/1",
			"Users/bobby/Literature/1",
		}
		items := make([]Item, len(paths))
		for i, path := range paths {
			items[i] = Item{Text: path, ID: i}
		}

		root, nodesMap := createTree(items, "/")

		if len(root.Children) != 1 {
			t.Errorf("root should have 1 children, got %d", len(root.Children))
		}

		if len(nodesMap) != 11 {
			t.Errorf("nodesMap should have 11 elements, got %d", len(nodesMap))
		}
	})
}
