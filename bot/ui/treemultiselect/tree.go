package treemultiselect

import (
	"fmt"
	"strings"
)

func createTree(items []*Item, separator string) (root *node, nodesMap map[string]*node) {
	nodesMap = make(map[string]*node)

	_counter := 0
	nextID := func() string {
		_counter++
		return fmt.Sprintf("t-%d", _counter) // t- prefix to avoid collisions with user-provided IDs
	}

	rootID := nextID()
	root = &node{
		Parent:   nil,
		Children: make(map[string]*node),
		Item:     &Item{ID: rootID},
	}
	nodesMap[rootID] = root

	for _, item := range items {
		item := item
		curr := root
		for {
			keys := strings.SplitN(item.Text, separator, 2)
			if len(keys) == 1 {
				item.Text = keys[0]
				newNode := &node{
					Parent:   curr,
					Children: make(map[string]*node),
					Item:     item,
				}
				curr.Children[item.Text] = newNode
				nodesMap[item.ID] = curr.Children[item.Text]
				break
			} else if len(keys) == 2 {
				newItem := &Item{Text: fmt.Sprintf("📁 %s", keys[0]), ID: nextID()}
				if existingNode, ok := curr.Children[newItem.Text]; !ok {
					newNode := &node{
						Parent:   curr,
						Children: make(map[string]*node),
						Item:     newItem,
					}
					curr.Children[newItem.Text] = newNode
					nodesMap[newItem.ID] = newNode
					curr = newNode
				} else {
					curr = existingNode
				}
				item.Text = keys[1]
			} else {
				panic("invalid item")
			}
		}
	}

	return root, nodesMap
}

type node struct {
	Parent   *node
	Children map[string]*node
	Item     *Item
}

func (n *node) isRoot() bool {
	return n.Parent == nil
}

func (n *node) isBranch() bool {
	return len(n.Children) > 0
}

func (n *node) isLeaf() bool {
	return len(n.Children) == 0
}
