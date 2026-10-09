package postgres

import (
	"fmt"
	"strings"

	"github.com/siia/siia-mcp/internal/contract"
	"github.com/siia/siia-mcp/internal/cursor"
	"github.com/siia/siia-mcp/internal/engine"
)

type keyedItem[T any] struct {
	item T
	key  cursor.SortKey
}

func (a *adapter) paginationContext(operation string, filters map[string]string) cursor.Context {
	return cursor.Context{
		Operation:    operation,
		Connection:   a.name,
		Filters:      filters,
		PolicyDigest: a.policyDigest,
	}
}

func (a *adapter) paginationStart(page engine.PageRequest, operation string, filters map[string]string, keyParts int) (cursor.SortKey, int, error) {
	limit, err := pageLimit(page, a.limit.MetadataPageSize)
	if err != nil {
		return cursor.SortKey{}, 0, err
	}
	if page.Cursor == nil {
		return cursor.SortKey{}, limit, nil
	}
	start, err := a.cursors.Decode(*page.Cursor, a.paginationContext(operation, filters))
	if err != nil || len(start.Values) != keyParts {
		return cursor.SortKey{}, 0, &contract.PublicError{Code: contract.CodeInvalidCursor, Message: "invalid cursor"}
	}
	return start, limit, nil
}

func finishPage[T any](a *adapter, items []keyedItem[T], limit int, operation string, filters map[string]string) (contract.Page[T], error) {
	out := contract.Page[T]{Items: []T{}}
	count := min(len(items), limit)
	for i := 0; i < count; i++ {
		out.Items = append(out.Items, items[i].item)
	}
	if len(items) > limit {
		token, err := a.cursors.Encode(a.paginationContext(operation, filters), items[limit-1].key)
		if err != nil {
			return contract.Page[T]{}, &contract.PublicError{Code: contract.CodeInternalError, Message: "cursor encoding failed"}
		}
		out.NextCursor = &token
	}
	return out, nil
}

func afterCursor(key, start cursor.SortKey) bool {
	if len(start.Values) == 0 {
		return true
	}
	for i := range key.Values {
		if cmp := strings.Compare(key.Values[i], start.Values[i]); cmp != 0 {
			return cmp > 0
		}
	}
	return key.OID > start.OID
}

func pageLimit(page engine.PageRequest, configured int) (int, error) {
	if page.Limit == 0 {
		return configured, nil
	}
	if page.Limit < 1 {
		return 0, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: "limit must be at least 1"}
	}
	if page.Limit > configured {
		return 0, &contract.PublicError{Code: contract.CodeInvalidRequest, Message: fmt.Sprintf("limit %d exceeds configured metadata page size %d", page.Limit, configured)}
	}
	return page.Limit, nil
}

func addMetadataFilter(filters map[string]string, key string, value *string) {
	if value != nil {
		filters[key] = *value
	}
}

func metadataFilters(schema string, table *string) map[string]string {
	filters := map[string]string{}
	if schema != "" {
		filters["schema"] = schema
	}
	if table != nil {
		filters["table"] = *table
	}
	return filters
}
