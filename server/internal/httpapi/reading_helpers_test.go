package httpapi

import (
	"strconv"

	"github.com/the-agency-labs/encompass/server/internal/reading"
	"github.com/the-agency-labs/encompass/server/internal/reading/readingtest"
)

func readingItoa(n int64) string { return strconv.FormatInt(n, 10) }

func readingtestBook(id, title, author string, pages int) readingtest.Book {
	return readingtest.Book{ID: id, Title: title, Author: author, Pages: pages}
}

func readingDocFields(title string) reading.DocFields {
	pages := int64(300)
	return reading.DocFields{Title: title, TotalPages: &pages}
}
