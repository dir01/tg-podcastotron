package treemultiselect

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/go-telegram/bot"
	"golang.org/x/exp/maps"
	"golang.org/x/exp/slices"
	"undercast-bot/bot/ui/multiselect"
)

type Item = multiselect.Item
type OnConfirmSelectionHandler = multiselect.OnConfirmSelectionHandler
type Option = func(tms *treeMultiSelect) multiselect.Option
type ActionButton = multiselect.ActionButton

var NewConfirmButton = multiselect.NewConfirmButton
var NewCancelButton = multiselect.NewCancelButton

func New(b *bot.Bot, items []*Item, onConfirmSelection OnConfirmSelectionHandler, opts ...Option) *multiselect.MultiSelect {
	for idx, item := range items {
		if item.ID == "" {
			item.ID = strconv.Itoa(idx)
		}
	}
	root, nodeMap := createTree(items, "/")
	tms := &treeMultiSelect{
		nodeMap:         nodeMap,
		root:            root,
		maxItemsPerPage: 10,
	}
	items = tms.prepareItems(root)
	itemFilters := tms.prepareItemFilters(root)

	var msOpts []multiselect.Option
	for _, opt := range opts {
		msOpts = append(msOpts, opt(tms))
	}

	msOpts = append(
		msOpts,
		multiselect.WithOnItemSelectedHandler(tms.onItemSelected),
		multiselect.WithItemFilters(itemFilters...),
	)

	return multiselect.New(b, items, onConfirmSelection, msOpts...)
}

type treeMultiSelect struct {
	nodeMap              map[string]*node
	root                 *node
	maxItemsPerPage      int
	dynamicActionButtons func(selectedItems []*Item) []ActionButton
}

func (tms *treeMultiSelect) getInitialItems() []*Item {
	return tms.prepareItems(tms.root)
}

func (tms *treeMultiSelect) onItemSelected(id string) *multiselect.StateChange {
	if strings.HasPrefix(id, "up:") {
		return tms.onUpBtnSelected(id)
	}

	node := tms.nodeMap[id]

	if node.isLeaf() {
		node.Item.Selected = !node.Item.Selected
		if tms.dynamicActionButtons == nil {
			return nil
		}
		actionButtons := tms.dynamicActionButtons(tms.getSelectedItems())
		return &multiselect.StateChange{
			ActionButtons: actionButtons,
		}
	} else {
		items := tms.prepareItems(node)
		items = tms.maybePrependUpBtn(items, node)
		newCurrPage := 0
		return &multiselect.StateChange{
			Items:       items,
			CurrentPage: &newCurrPage,
			ItemFilters: tms.prepareItemFilters(node),
		}
	}

}

func (tms *treeMultiSelect) onUpBtnSelected(id string) *multiselect.StateChange {
	parts := strings.Split(id, ":")
	if len(parts) != 3 {
		return nil
	}
	id = parts[1]
	node := tms.nodeMap[id]
	newCurrPage, _ := strconv.Atoi(parts[2])
	items := tms.prepareItems(node)
	items = tms.maybePrependUpBtn(items, node)
	return &multiselect.StateChange{
		Items:       items,
		CurrentPage: &newCurrPage,
		ItemFilters: tms.prepareItemFilters(node),
	}
}

func (tms *treeMultiSelect) prepareItems(node *node) []*Item {
	nodes := maps.Values(node.Children)
	items := make([]*Item, 0, len(nodes))
	for _, n := range nodes {
		items = append(items, n.Item)
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Text < items[j].Text
	})
	return items
}

func (tms *treeMultiSelect) maybePrependUpBtn(items []*Item, node *node) []*Item {
	if node.Parent == nil {
		return items
	}
	selectedNodeSiblingItems := tms.prepareItems(node.Parent)
	idx := slices.Index(selectedNodeSiblingItems, node.Item)
	page := (idx + 1) / tms.maxItemsPerPage
	items = append([]*Item{{ID: fmt.Sprintf("up:%s:%d", node.Parent.Item.ID, page), Text: "⬆️"}}, items...)
	return items
}

func (tms *treeMultiSelect) prepareItemFilters(n *node) []multiselect.ItemFilter {
	extCounter := make(map[string]int)
	for _, child := range n.Children {
		if !child.isLeaf() {
			continue
		}
		ext := filepath.Ext(child.Item.Text)
		if ext != "" {
			extCounter[ext]++
		}
	}

	topExt := ""
	topExtCount := 0
	for ext, count := range extCounter {
		if count > topExtCount {
			topExt = ext
			topExtCount = count
		}
	}

	if topExt != "" {
		return []multiselect.ItemFilter{
			{
				Text: "Select *" + topExt,
				Fn: func(item *Item) bool {
					return strings.HasSuffix(item.Text, topExt)
				},
			},
			multiselect.ItemFilterSelectNone,
		}
	}

	return []multiselect.ItemFilter{}
}

func (tms *treeMultiSelect) getSelectedItems() []*Item {
	// TODO: sort
	// TODO: map into initially provided values
	items := make([]*Item, 0)
	for _, v := range tms.nodeMap {
		if v.Item.Selected {
			items = append(items, v.Item)
		}
	}
	return items
}
