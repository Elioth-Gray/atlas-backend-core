package service

// Pagination limits applied to every list operation.
const (
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// PageParams selects one page of a list. Zero values fall back to the defaults.
type PageParams struct {
	Page     int
	PageSize int
}

// Normalize returns p with defaults applied and the page size capped at MaxPageSize.
func (p PageParams) Normalize() PageParams {
	if p.Page < 1 {
		p.Page = 1
	}
	if p.PageSize < 1 {
		p.PageSize = DefaultPageSize
	}
	if p.PageSize > MaxPageSize {
		p.PageSize = MaxPageSize
	}
	return p
}

// Offset returns the number of rows to skip for this page.
func (p PageParams) Offset() int { return (p.Page - 1) * p.PageSize }

// Page is one page of a list result.
type Page[T any] struct {
	Items    []T
	Total    int64
	Page     int
	PageSize int
}
