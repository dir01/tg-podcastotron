package treemultiselect

import (
	"strings"
)

func (tms *TreeMultiSelect) initializeTree(paths []string) {

	_counter := 0
	nextID := func() int {
		_counter++
		return _counter
	}

	tms.root = &TreeNode{
		Children: make(map[string]*TreeNode),
	}
	tms.currentNode = tms.root
	tms.nodeMap = make(map[int]*TreeNode)

	for _, pth := range paths {
		pth := pth
		curr := tms.root
		for {
			keys := strings.SplitN(pth, tms.separator, 2)

			if existingNode, ok := curr.Children[keys[0]]; ok {
				curr = existingNode
			} else {
				id := nextID()
				newNode := &TreeNode{
					Parent:   curr,
					Children: make(map[string]*TreeNode),
					ID:       id,
					Value:    keys[0],
					Text:     keys[0],
				}
				if tms.formatNode != nil {
					newNode.Text = tms.formatNode(newNode)
				}
				curr.Children[keys[0]] = newNode
				tms.nodeMap[id] = newNode
				curr = newNode
			}

			if len(keys) > 1 {
				pth = keys[1]
			} else {
				break
			}
		}
	}
}
