package multiselect

type Option func(ms *MultiSelect)

// WithItemFormatter allows to set function which will be called to set text of each item.
func WithItemFormatter(formatter func(item *Item) string) Option {
	return func(ms *MultiSelect) {
		ms.formatItem = formatter
	}
}

func WithMaxItemsPerPage(maxItemsPerPage int) Option {
	return func(ms *MultiSelect) {
		ms.maxItemsPerPage = maxItemsPerPage
	}
}

func WithOnItemSelectedHandler(f OnItemSelectedHandler) Option {
	return func(ms *MultiSelect) {
		ms.onItemSelectedHandler = f
	}
}

func WithItemFilters(filters ...ItemFilter) Option {
	return func(ms *MultiSelect) {
		ms.itemFilters = filters
	}
}

func WithActionButtons(buttons ...ActionButton) Option {
	return func(ms *MultiSelect) {
		ms.actionButtons = buttons
	}
}

// WithDeleteOnConfirmed sets the flag to delete the message on confirm.
func WithDeleteOnConfirmed(delete bool) Option {
	return func(ms *MultiSelect) {
		ms.deleteOnConfirmed = delete
	}
}

// WithDeleteOnCancel sets the flag to delete the message on cancel.
func WithDeleteOnCancel(delete bool) Option {
	return func(ms *MultiSelect) {
		ms.deleteOnCancel = delete
	}
}

// OnError sets the callback function for the error.
func OnError(f OnErrorHandler) Option {
	return func(ms *MultiSelect) {
		ms.onError = f
	}
}
