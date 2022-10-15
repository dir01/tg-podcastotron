package treemultiselect

import (
	"undercast-bot/bot/ui/multiselect"
)

func WithMaxItemsPerPage(i int) Option {
	return func(tp *treeMultiSelect) multiselect.Option {
		tp.maxItemsPerPage = i
		return multiselect.WithMaxItemsPerPage(i)
	}
}

func WithDynamicActionButtons(fn func(selectedItems []*Item) []ActionButton) Option {
	return func(tp *treeMultiSelect) multiselect.Option {
		tp.dynamicActionButtons = fn
		initialButtons := fn([]*Item{})
		return multiselect.WithActionButtons(initialButtons...)
	}
}
