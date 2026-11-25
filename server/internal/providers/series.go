package providers

// SeriesBook is one book of a series as a provider lists it.
type SeriesBook struct {
	Position   float64 `json:"position"`
	ExternalID string  `json:"external_id"`
	Title      string  `json:"title"`
	Image      string  `json:"image"`
	Year       int     `json:"year"`
}

// SeriesInfo is a series with every book the provider knows of it.
type SeriesInfo struct {
	ExternalID string       `json:"external_id"`
	Name       string       `json:"name"`
	Slug       string       `json:"slug"`
	BooksCount int          `json:"books_count"`
	Books      []SeriesBook `json:"books"`
}

// BookSeries is a series a book belongs to, with the book's position in it.
// Provider lists keep the provider's own order: the first is the primary one.
type BookSeries struct {
	Position float64    `json:"position"`
	Series   SeriesInfo `json:"series"`
}

// SeriesHit is one series search result.
type SeriesHit struct {
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
	BooksCount int    `json:"books_count"`
}
