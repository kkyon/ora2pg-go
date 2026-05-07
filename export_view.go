package main

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// VIEW and MVIEW export with metadata-driven approach

// VIEW functions

func loadViews(db *sql.DB) ([]*View, error) {
	rows, err := db.Query(`
SELECT view_name, text
FROM user_views
ORDER BY view_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	views := make([]*View, 0)
	for rows.Next() {
		var name, text string
		if err := rows.Scan(&name, &text); err != nil {
			return nil, err
		}

		// Load column list for this view from user_tab_columns
		columns, err := loadViewColumns(db, name)
		if err != nil {
			return nil, err
		}

		views = append(views, &View{
			Name:       name,
			Text:       text,
			ColumnList: columns,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(views, func(i, j int) bool { return views[i].Name < views[j].Name })
	return views, nil
}

func loadViewColumns(db *sql.DB, viewName string) ([]string, error) {
	rows, err := db.Query(`
SELECT column_name
FROM user_tab_columns
WHERE table_name = :1
ORDER BY column_id`, viewName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	columns := make([]string, 0)
	for rows.Next() {
		var colName string
		if err := rows.Scan(&colName); err != nil {
			return nil, err
		}
		columns = append(columns, strings.ToLower(colName))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return columns, nil
}

func renderViews(views []*View) string {
	var b strings.Builder

	for _, v := range views {
		b.WriteString("CREATE OR REPLACE VIEW ")
		b.WriteString(strings.ToLower(v.Name))

		if len(v.ColumnList) > 0 {
			b.WriteString(" (")
			for i, col := range v.ColumnList {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString(col)
			}
			b.WriteString(")")
		}

		b.WriteString(" AS ")
		b.WriteString(v.Text)

		if !strings.HasSuffix(v.Text, ";") {
			b.WriteString(";")
		}
		b.WriteString("\n\n")
	}

	b.WriteString("\n")
	return b.String()
}

// MVIEW functions

func loadMviews(db *sql.DB) ([]*Mview, error) {
	rows, err := db.Query(`
SELECT mview_name, query
FROM user_mviews
ORDER BY mview_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	mviews := make([]*Mview, 0)
	for rows.Next() {
		var name, query string
		if err := rows.Scan(&name, &query); err != nil {
			return nil, err
		}
		mviews = append(mviews, &Mview{
			Name:  name,
			Query: query,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(mviews, func(i, j int) bool { return mviews[i].Name < mviews[j].Name })
	return mviews, nil
}

func renderMviews(mviews []*Mview) string {
	var b strings.Builder

	for _, m := range mviews {
		b.WriteString("CREATE MATERIALIZED VIEW ")
		b.WriteString(strings.ToLower(m.Name))
		b.WriteString(" AS\n")

		// Convert Oracle TRUNC function to PostgreSQL date_trunc
		query := convertOracleToPostgreSQL(m.Query)

		b.WriteString(query)

		if !strings.HasSuffix(query, ";") {
			b.WriteString(";")
		}
		b.WriteString("\n\n")
	}

	b.WriteString("\n")
	return b.String()
}

// Helper functions

func convertOracleToPostgreSQL(query string) string {
	// Map Oracle TRUNC format codes to PostgreSQL date_trunc units
	formatMap := map[string]string{
		"'SYYYY'": "'year'",
		"'SYEAR'": "'year'",
		"'YEAR'":  "'year'",
		"'Y'":     "'year'",
		"'Q'":     "'quarter'",
		"'MONTH'": "'month'",
		"'MON'":   "'month'",
		"'MM'":    "'month'",
		"'RM'":    "'month'",
		"'IW'":    "'week'",
		"'DAY'":   "'week'",
		"'DY'":    "'week'",
		"'D'":     "'week'",
		"'DDD'":   "'day'",
		"'DD'":    "'day'",
		"'J'":     "'day'",
		"'HH'":    "'hour'",
		"'HH12'":  "'hour'",
		"'HH24'":  "'hour'",
		"'MI'":    "'minute'",
	}

	// Convert TRUNC(field, 'format') to date_trunc('unit', field)
	// This regex captures the field and format separately
	re := regexp.MustCompile(`(?i)TRUNC\s*\(\s*([^,]+?)\s*,\s*'([^']+)'\s*\)`)
	result := re.ReplaceAllStringFunc(query, func(match string) string {
		parts := re.FindStringSubmatch(match)
		if len(parts) >= 3 {
			field := strings.TrimSpace(parts[1])
			format := "'" + parts[2] + "'"

			// Look up the PostgreSQL unit
			pgUnit, ok := formatMap[strings.ToUpper(format)]
			if ok {
				return fmt.Sprintf("date_trunc(%s, %s)", pgUnit, field)
			}
		}
		return match
	})

	return result
}
