package main

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// TRIGGER export with metadata-driven approach.
// Current scope intentionally targets row-level table triggers and converts
// the common Oracle trigger patterns used in this repository.

func loadTriggers(db *sql.DB) ([]*Trigger, error) {
	rows, err := db.Query(`
SELECT trigger_name,
       table_name,
       triggering_event,
       trigger_type,
       trigger_body
FROM user_triggers
WHERE status = 'ENABLED'
  AND base_object_type = 'TABLE'
  AND trigger_type = 'BEFORE EACH ROW'
ORDER BY trigger_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	triggers := make([]*Trigger, 0)
	for rows.Next() {
		var name, tableName, event, trigType, body string
		if err := rows.Scan(&name, &tableName, &event, &trigType, &body); err != nil {
			return nil, err
		}

		triggers = append(triggers, &Trigger{
			Name:            name,
			TableName:       tableName,
			TriggeringEvent: event,
			TriggerType:     trigType,
			TriggerBody:     body,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(triggers, func(i, j int) bool { return triggers[i].Name < triggers[j].Name })
	return triggers, nil
}

func renderTriggers(triggers []*Trigger) string {
	var b strings.Builder

	for _, t := range triggers {
		triggerName := strings.ToLower(t.Name)
		tableName := strings.ToLower(t.TableName)
		funcName := "trigger_fct_" + triggerName

		b.WriteString(fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s CASCADE;\n", triggerName, tableName))
		b.WriteString(fmt.Sprintf("CREATE OR REPLACE FUNCTION app.%s() RETURNS trigger AS $BODY$\n", funcName))
		b.WriteString("BEGIN\n")
		b.WriteString(convertTriggerBody(t.TriggerBody))
		if !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
		b.WriteString("RETURN NEW;\n")
		b.WriteString("END\n")
		b.WriteString("$BODY$\n")
		b.WriteString(" LANGUAGE 'plpgsql' SECURITY DEFINER;\n")
		b.WriteString(fmt.Sprintf("-- REVOKE ALL ON FUNCTION app.%s() FROM PUBLIC;\n\n", funcName))

		eventClause := strings.ToUpper(strings.TrimSpace(t.TriggeringEvent))
		b.WriteString(fmt.Sprintf("CREATE TRIGGER %s\n", triggerName))
		b.WriteString(fmt.Sprintf("\tBEFORE %s ON %s FOR EACH ROW\n", eventClause, tableName))
		b.WriteString(fmt.Sprintf("\tEXECUTE PROCEDURE app.%s();\n\n", funcName))
	}

	return b.String()
}

func convertTriggerBody(body string) string {
	out := strings.TrimSpace(body)
	if out == "" {
		return ""
	}

	// Remove outer BEGIN/END wrapper when present; renderer re-wraps body.
	out = regexp.MustCompile(`(?is)^\s*BEGIN\s*`).ReplaceAllString(out, "")
	out = regexp.MustCompile(`(?is)\s*END\s*;?\s*$`).ReplaceAllString(out, "")

	// Oracle pseudo-records to PostgreSQL trigger records.
	out = regexp.MustCompile(`(?i):NEW\.`).ReplaceAllString(out, "NEW.")
	out = regexp.MustCompile(`(?i):OLD\.`).ReplaceAllString(out, "OLD.")

	// Oracle trigger operation keywords to PostgreSQL TG_OP checks.
	out = regexp.MustCompile(`(?i)\bINSERTING\b`).ReplaceAllString(out, "TG_OP = 'INSERT'")
	out = regexp.MustCompile(`(?i)\bUPDATING\b`).ReplaceAllString(out, "TG_OP = 'UPDATE'")
	out = regexp.MustCompile(`(?i)\bDELETING\b`).ReplaceAllString(out, "TG_OP = 'DELETE'")

	// Common built-in conversions.
	out = regexp.MustCompile(`(?i)\bSYSTIMESTAMP\b`).ReplaceAllString(out, "statement_timestamp()")

	// sequence_name.NEXTVAL -> nextval('sequence_name')
	nextValRe := regexp.MustCompile(`(?i)\b([A-Z0-9_]+)\.NEXTVAL\b`)
	out = nextValRe.ReplaceAllStringFunc(out, func(match string) string {
		parts := nextValRe.FindStringSubmatch(match)
		if len(parts) != 2 {
			return match
		}
		return fmt.Sprintf("nextval('%s')", strings.ToLower(parts[1]))
	})

	// Match Perl ora2pg trigger style for blank check semantics.
	// NEW.col IS NULL -> coalesce(NEW.col::text, '') = ''
	newNullRe := regexp.MustCompile(`(?i)\bNEW\.([A-Z0-9_]+)\s+IS\s+NULL\b`)
	out = newNullRe.ReplaceAllString(out, "coalesce(NEW.$1::text, '') = ''")

	return formatTriggerBodyLines(out)
}

func formatTriggerBodyLines(body string) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	formatted := make([]string, 0, len(lines)+4)
	inIf := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			formatted = append(formatted, "")
			continue
		}

		upper := strings.ToUpper(trimmed)
		switch {
		case strings.HasPrefix(upper, "IF "):
			formatted = append(formatted, "\t"+trimmed)
			inIf = true
		case upper == "END IF;":
			formatted = append(formatted, "\tEND IF;", "")
			inIf = false
		case strings.HasPrefix(trimmed, "NEW."):
			if inIf {
				formatted = append(formatted, "\t\t"+trimmed)
			} else {
				formatted = append(formatted, "\t"+trimmed)
			}
		default:
			if inIf {
				formatted = append(formatted, "\t\t"+trimmed)
			} else {
				formatted = append(formatted, "\t"+trimmed)
			}
		}
	}

	for len(formatted) > 0 && formatted[len(formatted)-1] == "" {
		formatted = formatted[:len(formatted)-1]
	}

	compressed := make([]string, 0, len(formatted))
	prevBlank := false
	for _, line := range formatted {
		isBlank := line == ""
		if isBlank && prevBlank {
			continue
		}
		compressed = append(compressed, line)
		prevBlank = isBlank
	}

	return strings.Join(compressed, "\n") + "\n"
}
