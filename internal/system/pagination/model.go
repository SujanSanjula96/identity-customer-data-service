package pagination

// Pagination is the page metadata of a list response: an item count, the page size, and the cursors
// of the next and previous pages.
type Pagination struct {
	Count          int    `json:"count"`
	PageSize       int    `json:"page_size"`
	NextCursor     string `json:"next_cursor,omitempty"`
	PreviousCursor string `json:"previous_cursor,omitempty"`
}
