package main

import (
	"database/sql"
	"sort"
	"strings"
)

// SYNONYM export with metadata-driven approach.
// Current repository baseline maps local synonyms to simple PostgreSQL views.

func loadSynonyms(db *sql.DB) ([]*Synonym, error) {
	rows, err := db.Query(`
SELECT synonym_name, table_name
FROM user_synonyms
WHERE db_link IS NULL
ORDER BY synonym_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	synonyms := make([]*Synonym, 0)
	for rows.Next() {
		var name, targetName string
		if err := rows.Scan(&name, &targetName); err != nil {
			return nil, err
		}
		synonyms = append(synonyms, &Synonym{
			Name:       name,
			TargetName: targetName,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(synonyms, func(i, j int) bool { return synonyms[i].Name < synonyms[j].Name })
	return synonyms, nil
}

func renderSynonyms(synonyms []*Synonym) string {
	var b strings.Builder

	for _, synonym := range synonyms {
		b.WriteString("CREATE OR REPLACE VIEW ")
		b.WriteString(strings.ToLower(synonym.Name))
		b.WriteString(" AS SELECT * FROM ")
		b.WriteString(strings.ToLower(synonym.TargetName))
		b.WriteString(";\n")
	}

	return b.String()
}
