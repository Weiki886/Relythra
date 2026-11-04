package api

import (
	"fmt"
	"net/http"
	"strconv"
)

type Page struct {
	Limit  int
	Offset int
}

func ParsePagination(r *http.Request, defaultLimit int, maxLimit int) (Page, error) {
	if defaultLimit <= 0 || maxLimit <= 0 || defaultLimit > maxLimit {
		return Page{}, fmt.Errorf("invalid pagination bounds")
	}

	limit, err := queryInt(r, "limit", defaultLimit)
	if err != nil || limit <= 0 || limit > maxLimit {
		return Page{}, fmt.Errorf("limit must be between 1 and %d", maxLimit)
	}
	offset, err := queryInt(r, "offset", 0)
	if err != nil || offset < 0 {
		return Page{}, fmt.Errorf("offset must be a non-negative integer")
	}
	return Page{Limit: limit, Offset: offset}, nil
}

func queryInt(r *http.Request, key string, fallback int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, err
	}
	return value, nil
}
