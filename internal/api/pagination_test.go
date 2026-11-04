package api

import (
	"net/http/httptest"
	"testing"
)

func TestParsePaginationUsesDefaultsAndBounds(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		page, err := ParsePagination(httptest.NewRequest("GET", "/items", nil), 20, 100)
		if err != nil {
			t.Fatalf("parse pagination: %v", err)
		}
		if page.Limit != 20 || page.Offset != 0 {
			t.Fatalf("pagination = %#v", page)
		}
	})

	t.Run("explicit", func(t *testing.T) {
		page, err := ParsePagination(
			httptest.NewRequest("GET", "/items?limit=100&offset=200", nil),
			20,
			100,
		)
		if err != nil {
			t.Fatalf("parse pagination: %v", err)
		}
		if page.Limit != 100 || page.Offset != 200 {
			t.Fatalf("pagination = %#v", page)
		}
	})

	for _, path := range []string{
		"/items?limit=0",
		"/items?limit=101",
		"/items?limit=abc",
		"/items?offset=-1",
		"/items?offset=abc",
	} {
		t.Run(path, func(t *testing.T) {
			if _, err := ParsePagination(httptest.NewRequest("GET", path, nil), 20, 100); err == nil {
				t.Fatalf("invalid pagination %q was accepted", path)
			}
		})
	}
}
