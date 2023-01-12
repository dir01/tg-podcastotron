package treemultiselect

type Option func(tms *TreeMultiSelect)

// WithNodeFormatter allows to set function which will be called to set text of each item.
func WithNodeFormatter(formatter func(item *TreeNode) string) Option {
	return func(tms *TreeMultiSelect) {
		tms.formatNode = formatter
	}
}

func WithMaxNodesPerPage(maxItemsPerPage int) Option {
	return func(tms *TreeMultiSelect) {
		tms.maxNodesPerPage = maxItemsPerPage
	}
}

func WithFilterButtons(filters ...FilterButton) Option {
	return func(tms *TreeMultiSelect) {
		tms.filterButtons = filters
	}
}

func WithDynamicFilterButtons(fn func(node []*TreeNode) []FilterButton) Option {
	return func(tms *TreeMultiSelect) {
		tms.dynamicFilterButtons = fn
		initialButtons := fn([]*TreeNode{})
		WithFilterButtons(initialButtons...)
	}
}

func WithActionButtons(buttons [][]ActionButton) Option {
	return func(tms *TreeMultiSelect) {
		tms.actionButtons = buttons
	}
}

func WithDynamicActionButtons(fn func(selectedNodes []*TreeNode) [][]ActionButton) Option {
	return func(tms *TreeMultiSelect) {
		tms.dynamicActionButtons = fn
		initialButtons := fn([]*TreeNode{})
		WithActionButtons(initialButtons)
	}
}

// WithDeleteOnConfirmed sets the flag to delete the message on confirm.
func WithDeleteOnConfirmed(delete bool) Option {
	return func(tms *TreeMultiSelect) {
		tms.deleteOnConfirmed = delete
	}
}

// WithDeleteOnCancel sets the flag to delete the message on cancel.
func WithDeleteOnCancel(delete bool) Option {
	return func(tms *TreeMultiSelect) {
		tms.deleteOnCancel = delete
	}
}

// OnError sets the callback function for the error.
func OnError(f OnErrorHandler) Option {
	return func(tms *TreeMultiSelect) {
		tms.onError = f
	}
}
