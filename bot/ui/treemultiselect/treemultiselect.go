package treemultiselect

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path"
	"sort"
	"sync"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
	"golang.org/x/exp/maps"
)

type OnConfirmSelectionHandler func(ctx context.Context, bot *bot.Bot, mes *models.Message, paths []string)
type OnCancelHandler func(ctx context.Context, bot *bot.Bot, mes *models.Message)
type OnErrorHandler func(err error)

type TreeNode struct {
	Parent   *TreeNode
	Children map[string]*TreeNode
	Text     string
	ID       int
	Value    string
	Selected bool
}

func (n *TreeNode) IsRoot() bool {
	return n.Parent == nil
}

func (n *TreeNode) IsBranch() bool {
	return len(n.Children) > 0
}

func (n *TreeNode) IsLeaf() bool {
	return len(n.Children) == 0
}

type TreeMultiSelect struct {
	// configurable params
	maxNodesPerPage      int
	deleteOnConfirmed    bool
	deleteOnCancel       bool
	formatNode           func(node *TreeNode) string
	formatUpBtn          func(node *TreeNode) string
	onError              OnErrorHandler
	filterButtons        []FilterButton
	actionButtons        [][]ActionButton
	dynamicActionButtons func([]*TreeNode) [][]ActionButton
	dynamicFilterButtons func([]*TreeNode) []FilterButton
	separator            string

	// data
	nodeMap     map[int]*TreeNode
	root        *TreeNode
	currentNode *TreeNode

	// internal
	prefix            string
	callbackHandlerID string
	currentPage       int
	prevPages         []int // stack of previous pages for "up" button opening the same page
	nodesLock         sync.RWMutex
}

func New(b *bot.Bot, paths []string, onConfirmSelection OnConfirmSelectionHandler, opts ...Option) *TreeMultiSelect {
	tms := &TreeMultiSelect{
		maxNodesPerPage:   10,
		separator:         "/",
		deleteOnConfirmed: true,
		deleteOnCancel:    true,

		formatNode: func(node *TreeNode) string {
			if node.Selected {
				return "☑️ " + node.Text
			} else if node.IsBranch() {
				return "📁 " + node.Text
			} else {
				return node.Text
			}
		},
		formatUpBtn: func(node *TreeNode) string {
			return "🔼"
		},

		filterButtons:        []FilterButton{FilterButtonSelectAll, FilterButtonSelectNone},
		dynamicFilterButtons: nil,

		actionButtons: [][]ActionButton{
			{NewCancelButton("Cancel", defaultOnCancel)},
			{NewConfirmButton("Confirm", onConfirmSelection)},
		},
		dynamicActionButtons: nil,

		onError: defaultOnError,
		prefix:  bot.RandomString(16),
	}
	tms.initializeTree(paths)

	for _, opt := range opts {
		opt(tms)
	}

	tms.callbackHandlerID = b.RegisterHandler(bot.HandlerTypeCallbackQueryData, tms.prefix, bot.MatchTypePrefix, tms.callback)

	return tms
}

func (tms *TreeMultiSelect) MarshalJSON() ([]byte, error) {
	return json.Marshal(&models.InlineKeyboardMarkup{InlineKeyboard: tms.buildKeyboard()})
}

func (tms *TreeMultiSelect) prepareResults() []string {
	nodes := tms.getAllSelectedNodes()
	var result []string
	for _, node := range nodes {
		result = append(result, nodeToPath(node))
	}
	return result
}

func (tms *TreeMultiSelect) getAllSelectedNodes() []*TreeNode {
	var nodes []*TreeNode
	for _, node := range tms.nodeMap {
		if !node.Selected {
			continue
		}
		nodes = append(nodes, node)
	}
	return nodes
}

func (tms *TreeMultiSelect) goUp(ctx context.Context, b *bot.Bot, message *models.Message, prevPaginationPosition int) {
	if tms.currentNode.IsRoot() {
		tms.onError(fmt.Errorf("can't go up from root node"))
		return
	}
	tms.currentNode = tms.currentNode.Parent
	tms.currentPage = prevPaginationPosition
	tms.prevPages = tms.prevPages[:len(tms.prevPages)-1]
	if tms.dynamicFilterButtons != nil {
		tms.filterButtons = tms.dynamicFilterButtons(maps.Values(tms.currentNode.Children))
	}
	tms.sendUpdatedMarkup(ctx, b, message)
}

func nodeToPath(node *TreeNode) string {
	var pathParts []string
	for !node.IsRoot() {
		pathParts = append(pathParts, node.Value)
		node = node.Parent
	}
	sort.SliceStable(pathParts, func(i, j int) bool {
		return i > j
	})
	fullPath := path.Join(pathParts...)
	return fullPath
}

func defaultOnError(err error) {
	log.Printf("[TG-UI-MULTISELECT] [ERROR] %s", err)
}

func defaultOnCancel(_ context.Context, _ *bot.Bot, _ *models.Message) {}
