package main

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type ReportRow struct {
	ObjectType   string
	Number       int
	Invalid      int
	Compatible   string
	MappingNotes string
}

type ShowReport struct {
	Version   string
	Schema    string
	Generated string
	Rows      []ReportRow
	TypeRules []TableTypeRule
	TotalNum  int
	TotalBad  int
}

type TableTypeRule struct {
	OracleType string
	ColumnCnt  int
	Compatible string
	PgMapping  string
	Solution   string
}

type reportMapping struct {
	ObjectType   string
	ExportType   string
	Compatible   string
	MappingNotes string
}

var showReportMappings = []reportMapping{
	{ObjectType: "TABLE", ExportType: "TABLE", Compatible: "yes", MappingNotes: "Mapped to PostgreSQL CREATE TABLE with constraints and indexes where available."},
	{ObjectType: "SEQUENCE", ExportType: "SEQUENCE", Compatible: "yes", MappingNotes: "Mapped to PostgreSQL CREATE SEQUENCE syntax."},
	{ObjectType: "VIEW", ExportType: "VIEW", Compatible: "yes", MappingNotes: "Mapped to PostgreSQL CREATE VIEW with Oracle SQL normalization where implemented."},
	{ObjectType: "MATERIALIZED VIEW", ExportType: "MVIEW", Compatible: "yes", MappingNotes: "Mapped to PostgreSQL materialized view output."},
	{ObjectType: "TYPE", ExportType: "TYPE", Compatible: "partial", MappingNotes: "Object and nested-table style types are mapped; advanced Oracle type behaviors may need manual rewrite."},
	{ObjectType: "FUNCTION", ExportType: "FUNCTION", Compatible: "partial", MappingNotes: "Function signatures and bodies are converted with best effort; manual review is required."},
	{ObjectType: "PROCEDURE", ExportType: "PROCEDURE", Compatible: "partial", MappingNotes: "Procedure signatures and bodies are converted with best effort; manual review is required."},
	{ObjectType: "PACKAGE BODY", ExportType: "PACKAGE", Compatible: "partial", MappingNotes: "Package members are flattened into PostgreSQL objects; package semantics require manual validation."},
	{ObjectType: "TRIGGER", ExportType: "TRIGGER", Compatible: "partial", MappingNotes: "Row-level BEFORE table triggers are exported with common PL/SQL conversions; complex trigger patterns still require manual validation."},
	{ObjectType: "SYNONYM", ExportType: "SYNONYM", Compatible: "partial", MappingNotes: "Local synonyms are exported as simple PostgreSQL views over the target object; complex synonym cases still require manual validation."},
	{ObjectType: "DATABASE LINK", ExportType: "n/a", Compatible: "no", MappingNotes: "No database link exporter in ora2pg-go yet. Typical mapping target is FDW/oracle_fdw."},
}

func loadShowReport(db *sql.DB) (*ShowReport, error) {
	report := &ShowReport{Generated: time.Now().UTC().Format(time.RFC3339)}

	report.Version = loadOracleVersion(db)
	report.Schema = loadCurrentSchema(db)

	rows := make([]ReportRow, 0, len(showReportMappings))
	for _, m := range showReportMappings {
		count, invalid, err := loadObjectCounters(db, m.ObjectType)
		if err != nil {
			return nil, err
		}

		row := ReportRow{
			ObjectType:   m.ObjectType,
			Number:       count,
			Invalid:      invalid,
			Compatible:   m.Compatible,
			MappingNotes: m.MappingNotes,
		}
		rows = append(rows, row)
		report.TotalNum += count
		report.TotalBad += invalid
	}

	report.Rows = rows

	typeRules, err := loadTableTypeRules(db)
	if err != nil {
		return nil, err
	}
	report.TypeRules = typeRules

	return report, nil
}

func loadTableTypeRules(db *sql.DB) ([]TableTypeRule, error) {
	rows, err := db.Query(`
SELECT c.data_type, COUNT(*)
FROM user_tab_cols c
WHERE c.hidden_column = 'NO'
  AND c.table_name IN (
    SELECT t.table_name
    FROM user_tables t
    WHERE t.nested = 'NO'
      AND NVL(t.secondary, 'N') = 'N'
      AND t.table_name NOT IN (SELECT mview_name FROM user_mviews)
  )
GROUP BY c.data_type
ORDER BY c.data_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rules := make([]TableTypeRule, 0)
	for rows.Next() {
		var oraType string
		var cnt int
		if err := rows.Scan(&oraType, &cnt); err != nil {
			return nil, err
		}

		compat, pgMap, solution := assessTableDataTypeRule(oraType)
		rules = append(rules, TableTypeRule{
			OracleType: oraType,
			ColumnCnt:  cnt,
			Compatible: compat,
			PgMapping:  pgMap,
			Solution:   solution,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return rules, nil
}

func assessTableDataTypeRule(oraType string) (compatible string, pgMapping string, solution string) {
	t := strings.ToUpper(strings.TrimSpace(oraType))
	if strings.HasPrefix(t, "TIMESTAMP") {
		return "yes", "timestamp", "Use PostgreSQL timestamp/timestamptz policy for timezone handling."
	}

	switch t {
	case "NUMBER", "INTEGER", "SMALLINT", "FLOAT", "BINARY_FLOAT", "BINARY_DOUBLE", "DECIMAL":
		return "yes", mapOracleToPgType(t), "Use numeric/integer mapping based on precision and scale policy."
	case "VARCHAR2", "NVARCHAR2", "VARCHAR", "CHAR", "NCHAR":
		return "yes", mapOracleToPgType(t), "Keep character length semantics and collation policy under review."
	case "DATE":
		return "yes", "timestamp(0)", "Oracle DATE includes time; map to timestamp(0) for closer behavior."
	case "CLOB", "NCLOB":
		return "yes", "text", "Map large text to PostgreSQL text and review application size assumptions."
	case "BLOB", "RAW", "LONG RAW":
		return "partial", "bytea", "Map binary payload to bytea and validate streaming/large object handling."
	case "XMLTYPE":
		return "partial", "xml or text", "Prefer PostgreSQL xml where possible, fallback to text for unsupported operations."
	case "ROWID", "UROWID":
		return "no", "text", "No direct PostgreSQL ROWID equivalent; persist as text only if business logic needs it."
	default:
		mapped := mapOracleToPgType(t)
		if mapped == strings.ToLower(t) {
			return "review", mapped, "No explicit built-in rule. Review and define a custom mapping before migration."
		}
		return "partial", mapped, "Mapped with generic conversion. Validate precision/semantics manually."
	}
}

func loadOracleVersion(db *sql.DB) string {
	var version string
	err := db.QueryRow(`
SELECT product || ' ' || version
FROM product_component_version
WHERE product LIKE 'Oracle Database%'
FETCH FIRST 1 ROWS ONLY`).Scan(&version)
	if err == nil && strings.TrimSpace(version) != "" {
		return version
	}

	err = db.QueryRow(`SELECT banner FROM v$version WHERE rownum = 1`).Scan(&version)
	if err == nil && strings.TrimSpace(version) != "" {
		return version
	}

	return "Oracle Database"
}

func loadCurrentSchema(db *sql.DB) string {
	var schema string
	err := db.QueryRow(`SELECT SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') FROM dual`).Scan(&schema)
	if err != nil || strings.TrimSpace(schema) == "" {
		return "UNKNOWN"
	}
	return schema
}

func loadObjectCounters(db *sql.DB, objectType string) (int, int, error) {
	switch objectType {
	case "TABLE":
		count, err := queryCount(db, `
SELECT COUNT(*)
FROM user_tables t
WHERE t.nested = 'NO'
  AND NVL(t.secondary, 'N') = 'N'
  AND t.table_name NOT IN (SELECT mview_name FROM user_mviews)`)
		return count, 0, err
	case "MATERIALIZED VIEW":
		count, err := queryCount(db, `SELECT COUNT(*) FROM user_mviews`)
		return count, 0, err
	case "DATABASE LINK":
		count, err := queryCount(db, `SELECT COUNT(*) FROM user_db_links`)
		return count, 0, err
	default:
		count, err := queryCount(db, `SELECT COUNT(*) FROM user_objects WHERE object_type = :1`, objectType)
		if err != nil {
			return 0, 0, err
		}
		invalid, err := queryCount(db, `
SELECT COUNT(*)
FROM user_objects
WHERE object_type = :1
  AND status <> 'VALID'`, objectType)
		if err != nil {
			return 0, 0, err
		}
		return count, invalid, nil
	}
}

func queryCount(db *sql.DB, sqlText string, args ...any) (int, error) {
	var n int
	if err := db.QueryRow(sqlText, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

func renderShowReportMarkdown(report *ShowReport) string {
	var b strings.Builder
	b.WriteString("# Ora2Pg-Go Compatibility Report\n\n")
	b.WriteString("This report is a compatibility and object-mapping reminder only.\n")
	b.WriteString("Cost estimation is intentionally not implemented in ora2pg-go.\n\n")
	b.WriteString(fmt.Sprintf("- Generated: %s\n", report.Generated))
	b.WriteString(fmt.Sprintf("- Version: %s\n", report.Version))
	b.WriteString(fmt.Sprintf("- Schema: %s\n\n", report.Schema))

	b.WriteString("## Object Summary\n\n")
	b.WriteString("| Object | Number | Invalid | Compatibility | Mapping Reminder |\n")
	b.WriteString("|---|---:|---:|---|---|\n")
	for _, row := range report.Rows {
		b.WriteString(fmt.Sprintf("| %s | %d | %d | %s | %s |\n",
			row.ObjectType,
			row.Number,
			row.Invalid,
			row.Compatible,
			escapePipe(row.MappingNotes),
		))
	}
	b.WriteString(fmt.Sprintf("| **Total** | **%d** | **%d** | - | Compatibility-only totals (no cost units) |\n\n", report.TotalNum, report.TotalBad))

	b.WriteString("## Object Mapping Reminder\n\n")
	for _, m := range showReportMappings {
		b.WriteString(fmt.Sprintf("- %s -> %s: %s\n", m.ObjectType, m.ExportType, m.MappingNotes))
	}

	b.WriteString("\n## Table Data Type Compatibility Rules\n\n")
	b.WriteString("| Oracle Data Type | Columns | Compatibility | PostgreSQL Mapping | Mapping Solution |\n")
	b.WriteString("|---|---:|---|---|---|\n")
	for _, r := range report.TypeRules {
		b.WriteString(fmt.Sprintf("| %s | %d | %s | %s | %s |\n",
			r.OracleType,
			r.ColumnCnt,
			r.Compatible,
			escapePipe(r.PgMapping),
			escapePipe(r.Solution),
		))
	}

	return b.String()
}

func escapePipe(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}
