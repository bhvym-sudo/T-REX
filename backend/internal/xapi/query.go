package xapi

import (
	"fmt"
	"strings"

	"trex/backend/internal/model"
)

func BuildQuery(request model.ScanRequest) (string, error) {
	if request.Mode == "custom" {
		query := strings.TrimSpace(request.CustomQuery)
		if query == "" {
			return "", fmt.Errorf("custom query is required")
		}
		if err := ValidateQuery(query); err != nil {
			return "", err
		}
		return query, nil
	}
	terms := cleanTerms(request.Terms)
	if len(terms) == 0 {
		return "", fmt.Errorf("enter at least one keyword or account")
	}
	joiner := " OR "
	if strings.EqualFold(request.MatchMode, "AND") {
		joiner = " "
	}
	if request.Mode == "accounts" {
		accounts := make([]string, 0, len(terms))
		for _, term := range terms {
			accounts = append(accounts, "from:"+strings.TrimLeft(term, "@"))
		}
		query := "(" + strings.Join(accounts, " OR ") + ")"
		filters := cleanTerms(request.AccountFilters)
		if len(filters) > 0 {
			query += " (" + strings.Join(filters, joiner) + ")"
		}
		return addDates(query, request.FromDate, request.ToDate), nil
	}
	return addDates("("+strings.Join(terms, joiner)+")", request.FromDate, request.ToDate), nil
}

func ValidateQuery(query string) error {
	depth := 0
	quoted := false
	for _, char := range query {
		switch char {
		case '"':
			quoted = !quoted
		case '(':
			if !quoted {
				depth++
			}
		case ')':
			if !quoted {
				depth--
				if depth < 0 {
					return fmt.Errorf("custom query has a closing bracket without an opening bracket")
				}
			}
		case '\n', '\r':
			return fmt.Errorf("custom query must be on one line")
		}
	}
	if quoted {
		return fmt.Errorf("custom query has an unclosed quote")
	}
	if depth != 0 {
		return fmt.Errorf("custom query has unbalanced brackets")
	}
	return nil
}

func cleanTerms(values []string) []string {
	result := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if strings.Contains(value, " ") && !strings.HasPrefix(value, "\"") {
			value = "\"" + strings.Trim(value, "\"") + "\""
		}
		result = append(result, value)
	}
	return result
}

func addDates(query, from, to string) string {
	if from != "" {
		query += " since:" + from
	}
	if to != "" {
		query += " until:" + to
	}
	return query
}
