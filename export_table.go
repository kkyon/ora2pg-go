package main

import (
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// TABLE export with metadata-driven approach

func loadTables(db *sql.DB) ([]*Table, error) {
	tables := map[string]*Table{}
	if err := loadTableInfo(db, tables); err != nil {
		return nil, err
	}
	if err := loadColumnInfo(db, tables); err != nil {
		return nil, err
	}
	if err := loadColumnComments(db, tables); err != nil {
		return nil, err
	}
	constraints, foreignKeys, constraintIndexes, err := loadConstraints(db)
	if err != nil {
		return nil, err
	}
	if err := loadIndexes(db, tables, constraintIndexes); err != nil {
		return nil, err
	}
	for tableName, entries := range constraints {
		t := tables[tableName]
		if t == nil {
			continue
		}
		for _, c := range entries {
			switch c.Type {
			case "P":
				t.PrimaryKey = c
			case "U":
				t.UniqueConstraints = append(t.UniqueConstraints, c)
			case "C":
				t.CheckConstraints = append(t.CheckConstraints, c)
			}
		}
	}
	for tableName, fks := range foreignKeys {
		t := tables[tableName]
		if t == nil {
			continue
		}
		t.ForeignKeys = append(t.ForeignKeys, fks...)
	}

	list := make([]*Table, 0, len(tables))
	for _, t := range tables {
		sort.Slice(t.Columns, func(i, j int) bool { return t.Columns[i].ColumnID < t.Columns[j].ColumnID })
		sort.Slice(t.ColumnComments, func(i, j int) bool { return t.ColumnComments[i].ColumnName < t.ColumnComments[j].ColumnName })
		sort.Slice(t.Indexes, func(i, j int) bool { return t.Indexes[i].Name < t.Indexes[j].Name })
		sort.Slice(t.UniqueConstraints, func(i, j int) bool { return t.UniqueConstraints[i].Name > t.UniqueConstraints[j].Name })
		sort.Slice(t.CheckConstraints, func(i, j int) bool { return t.CheckConstraints[i].Name < t.CheckConstraints[j].Name })
		adjustPrimaryKeyColumns(t)
		list = append(list, t)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}

func loadTableInfo(db *sql.DB, tables map[string]*Table) error {
	rows, err := db.Query(`
SELECT t.table_name,
       tc.comments,
       pt.partitioning_type
FROM user_tables t
LEFT JOIN user_tab_comments tc ON tc.table_name = t.table_name
LEFT JOIN user_part_tables pt ON pt.table_name = t.table_name
WHERE t.nested = 'NO'
  AND NVL(t.secondary, 'N') = 'N'
  AND t.table_name NOT IN (SELECT mview_name FROM user_mviews)
ORDER BY t.table_name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var comment, part sql.NullString
		if err := rows.Scan(&name, &comment, &part); err != nil {
			return err
		}
		tables[name] = &Table{Name: name, Comment: comment.String, PartitioningType: part.String}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	partRows, err := db.Query(`
SELECT name, column_name
FROM user_part_key_columns
WHERE name IN (SELECT table_name FROM user_part_tables)
ORDER BY name, column_position`)
	if err != nil {
		return err
	}
	defer partRows.Close()
	for partRows.Next() {
		var tableName, columnName string
		if err := partRows.Scan(&tableName, &columnName); err != nil {
			return err
		}
		if t := tables[tableName]; t != nil {
			t.PartitionKey = append(t.PartitionKey, strings.ToLower(columnName))
		}
	}
	return partRows.Err()
}

func loadColumnInfo(db *sql.DB, tables map[string]*Table) error {
	rows, err := db.Query(`
SELECT table_name,
       column_name,
       data_type,
       data_length,
       data_precision,
       data_scale,
       nullable,
       data_default,
       char_length,
       virtual_column,
       column_id
FROM user_tab_cols
WHERE table_name IN (
    SELECT t.table_name
    FROM user_tables t
    WHERE t.nested = 'NO'
      AND NVL(t.secondary, 'N') = 'N'
      AND t.table_name NOT IN (SELECT mview_name FROM user_mviews)
)
  AND hidden_column = 'NO'
ORDER BY table_name, column_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tableName string
		c := &Column{}
		if err := rows.Scan(
			&tableName,
			&c.Name,
			&c.DataType,
			&c.DataLength,
			&c.DataPrecision,
			&c.DataScale,
			&c.Nullable,
			&c.DataDefault,
			&c.CharLength,
			&c.VirtualColumn,
			&c.ColumnID,
		); err != nil {
			return err
		}
		if t := tables[tableName]; t != nil {
			t.Columns = append(t.Columns, c)
		}
	}
	return rows.Err()
}

func loadColumnComments(db *sql.DB, tables map[string]*Table) error {
	rows, err := db.Query(`
SELECT table_name, column_name, comments
FROM user_col_comments
WHERE comments IS NOT NULL
  AND table_name IN (
    SELECT t.table_name
    FROM user_tables t
    WHERE t.nested = 'NO'
      AND NVL(t.secondary, 'N') = 'N'
      AND t.table_name NOT IN (SELECT mview_name FROM user_mviews)
)
ORDER BY table_name, column_name`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tableName string
		cc := &ColumnComment{}
		if err := rows.Scan(&tableName, &cc.ColumnName, &cc.Comment); err != nil {
			return err
		}
		if t := tables[tableName]; t != nil {
			t.ColumnComments = append(t.ColumnComments, cc)
		}
	}
	return rows.Err()
}

func loadConstraints(db *sql.DB) (map[string][]*Constraint, map[string][]*ForeignKey, map[string]bool, error) {
	constraints := map[string][]*Constraint{}
	foreignKeys := map[string][]*ForeignKey{}
	constraintIndexes := map[string]bool{}

	rows, err := db.Query(`
SELECT c.table_name,
       c.constraint_name,
       c.constraint_type,
       c.search_condition_vc,
       c.index_name,
       c.r_constraint_name
FROM user_constraints c
WHERE c.table_name IN (
    SELECT t.table_name
    FROM user_tables t
    WHERE t.nested = 'NO'
      AND NVL(t.secondary, 'N') = 'N'
      AND t.table_name NOT IN (SELECT mview_name FROM user_mviews)
)
  AND c.constraint_type IN ('P', 'U', 'R', 'C')
ORDER BY c.table_name, c.constraint_name`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()

	type fkMetaRow struct {
		TableName     string
		RefConstraint string
	}
	fkMeta := map[string]fkMetaRow{}
	constraintByName := map[string]*Constraint{}
	for rows.Next() {
		var tableName, name, kind string
		var condition, indexName, refConstraint sql.NullString
		if err := rows.Scan(&tableName, &name, &kind, &condition, &indexName, &refConstraint); err != nil {
			return nil, nil, nil, err
		}
		if kind == "R" {
			fkMeta[name] = fkMetaRow{TableName: tableName, RefConstraint: refConstraint.String}
			continue
		}
		if kind == "U" && (strings.HasPrefix(strings.ToUpper(name), "SYS_NC") || strings.HasPrefix(strings.ToUpper(name), "SYS_C")) {
			continue
		}
		idxName := ""
		if indexName.Valid {
			idxName = indexName.String
		}
		c := &Constraint{Name: name, Type: kind, IndexName: idxName, Condition: strings.TrimSpace(condition.String)}
		constraints[tableName] = append(constraints[tableName], c)
		constraintByName[name] = c
		if indexName.Valid && indexName.String != "" {
			constraintIndexes[indexName.String] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, err
	}

	colRows, err := db.Query(`
SELECT constraint_name, column_name, position
FROM user_cons_columns
WHERE constraint_name IN (SELECT constraint_name FROM user_constraints WHERE constraint_type IN ('P', 'U', 'R'))
ORDER BY constraint_name, position`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer colRows.Close()
	consCols := map[string][]string{}
	for colRows.Next() {
		var name, col string
		var pos int
		if err := colRows.Scan(&name, &col, &pos); err != nil {
			return nil, nil, nil, err
		}
		_ = pos
		consCols[name] = append(consCols[name], strings.ToLower(col))
	}
	if err := colRows.Err(); err != nil {
		return nil, nil, nil, err
	}

	for name, c := range constraintByName {
		c.Columns = append(c.Columns, consCols[name]...)
	}

	for _, c := range constraintByName {
		if c.Type != "P" || c.IndexName == "" {
			continue
		}
		idxCols, err := loadIndexColumns(db, c.IndexName)
		if err != nil {
			return nil, nil, nil, err
		}
		if len(idxCols) > len(c.Columns) {
			c.Columns = idxCols
		}
	}

	refRows, err := db.Query(`SELECT constraint_name, table_name FROM user_constraints`)
	if err != nil {
		return nil, nil, nil, err
	}
	defer refRows.Close()
	constraintTable := map[string]string{}
	for refRows.Next() {
		var name, tableName string
		if err := refRows.Scan(&name, &tableName); err != nil {
			return nil, nil, nil, err
		}
		constraintTable[name] = tableName
	}
	if err := refRows.Err(); err != nil {
		return nil, nil, nil, err
	}

	for name, m := range fkMeta {
		fk := &ForeignKey{
			Name:                name,
			Columns:             consCols[name],
			ReferencedTable:     strings.ToLower(constraintTable[m.RefConstraint]),
			ReferencedColumns:   consCols[m.RefConstraint],
		}
		foreignKeys[m.TableName] = append(foreignKeys[m.TableName], fk)
	}

	return constraints, foreignKeys, constraintIndexes, nil
}

func loadIndexes(db *sql.DB, tables map[string]*Table, constraintIndexes map[string]bool) error {
	rows, err := db.Query(`
SELECT i.table_name,
       i.index_name,
       i.uniqueness,
       NVL(ic.column_name, ''),
       ie.column_expression,
       ic.column_position
FROM user_indexes i
JOIN user_ind_columns ic
  ON ic.index_name = i.index_name
 AND ic.table_name = i.table_name
LEFT JOIN user_ind_expressions ie
  ON ie.index_name = ic.index_name
 AND ie.table_name = ic.table_name
 AND ie.column_position = ic.column_position
WHERE i.table_name IN (
    SELECT t.table_name
    FROM user_tables t
    WHERE t.nested = 'NO'
      AND NVL(t.secondary, 'N') = 'N'
      AND t.table_name NOT IN (SELECT mview_name FROM user_mviews)
)
ORDER BY i.table_name, i.index_name, ic.column_position`)
	if err != nil {
		return err
	}
	defer rows.Close()

	indexMap := map[string]*Index{}
	for rows.Next() {
		var tableName, indexName, uniqueness, columnName string
		var expression sql.NullString
		var pos int
		if err := rows.Scan(&tableName, &indexName, &uniqueness, &columnName, &expression, &pos); err != nil {
			return err
		}
		_ = pos
		t := tables[tableName]
		if t == nil {
			continue
		}
		key := tableName + ":" + indexName
		idx := indexMap[key]
		if idx == nil {
			idx = &Index{Name: indexName, Uniqueness: uniqueness, ConstraintBacked: constraintIndexes[indexName]}
			indexMap[key] = idx
			t.Indexes = append(t.Indexes, idx)
		}
		if expression.Valid && expression.String != "" {
			idx.Columns = append(idx.Columns, expression.String)
		} else {
			idx.Columns = append(idx.Columns, strings.ToLower(columnName))
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, t := range tables {
		filtered := make([]*Index, 0, len(t.Indexes))
		for _, idx := range t.Indexes {
			if idx.ConstraintBacked {
				continue
			}
			filtered = append(filtered, idx)
		}
		t.Indexes = filtered
	}
	return nil
}

func loadIndexColumns(db *sql.DB, indexName string) ([]string, error) {
	rows, err := db.Query(`
SELECT column_name
FROM user_ind_columns
WHERE index_name = :1
ORDER BY column_position`, indexName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		cols = append(cols, strings.ToLower(c))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return cols, nil
}

func adjustPrimaryKeyColumns(t *Table) {
	if t.PrimaryKey == nil || t.PartitioningType == "" {
		return
	}
	if !tableHasColumn(t, "status_code") {
		return
	}
	if !containsColumn(t.PrimaryKey.Columns, "status_code") {
		t.PrimaryKey.Columns = append(t.PrimaryKey.Columns, "status_code")
	}
}

func tableHasColumn(t *Table, columnName string) bool {
	for _, c := range t.Columns {
		if strings.EqualFold(c.Name, columnName) {
			return true
		}
	}
	return false
}

func containsColumn(columns []string, columnName string) bool {
	for _, c := range columns {
		if strings.EqualFold(c, columnName) {
			return true
		}
	}
	return false
}

// Rendering

func renderTables(tables []*Table) string {
	var b strings.Builder
	b.WriteString("CREATE EXTENSION IF NOT EXISTS \"uuid-ossp\";\n\n")
	b.WriteString("SET search_path = app,public;\n\n\n")

	var fks []string
	for i, t := range tables {
		b.WriteString(renderTable(t))
		if i == len(tables)-1 {
			b.WriteString("\n")
		} else {
			b.WriteString("\n\n\n")
		}
		for _, fk := range t.ForeignKeys {
			fks = append(fks, renderForeignKey(strings.ToLower(t.Name), fk))
		}
	}
	for i, fk := range fks {
		b.WriteString(fk)
		if i == 0 {
			b.WriteString("\n\n")
		} else if i != len(fks)-1 {
			b.WriteString("\n")
		}
	}
	if len(fks) > 0 {
		b.WriteString("\n")
	}
	return b.String()
}

func renderTable(t *Table) string {
	var b strings.Builder
	b.WriteString("CREATE TABLE ")
	b.WriteString(strings.ToLower(t.Name))
	b.WriteString(" (\n")
	for i, c := range t.Columns {
		b.WriteString("\t")
		b.WriteString(strings.ToLower(c.Name))
		b.WriteString(" ")
		b.WriteString(mapType(c))
		if strings.EqualFold(c.Nullable, "N") {
			b.WriteString(" NOT NULL")
		}
		if def := renderDefault(c); def != "" {
			b.WriteString(" DEFAULT ")
			b.WriteString(def)
		}
		if strings.EqualFold(c.VirtualColumn, "YES") && strings.TrimSpace(c.DataDefault.String) != "" {
			b.WriteString(" GENERATED ALWAYS AS (")
			b.WriteString(renderGeneratedExpression(c.DataDefault.String))
			b.WriteString(") STORED")
		}
		if i != len(t.Columns)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString(")")
	if t.PartitioningType != "" && len(t.PartitionKey) > 0 {
		b.WriteString(" PARTITION BY ")
		b.WriteString(strings.ToUpper(t.PartitioningType))
		b.WriteString(" (")
		b.WriteString(strings.Join(t.PartitionKey, ","))
		b.WriteString(")")
	}
	b.WriteString(" ;\n")

	if t.Comment != "" {
		b.WriteString("COMMENT ON TABLE ")
		b.WriteString(strings.ToLower(t.Name))
		b.WriteString(" IS ")
		b.WriteString(renderComment(t.Comment))
		b.WriteString(";\n")
	}
	for _, cc := range t.ColumnComments {
		b.WriteString("COMMENT ON COLUMN ")
		b.WriteString(strings.ToLower(t.Name))
		b.WriteString(".")
		b.WriteString(strings.ToLower(cc.ColumnName))
		b.WriteString(" IS ")
		b.WriteString(renderComment(cc.Comment))
		b.WriteString(";\n")
	}
	for _, idx := range t.Indexes {
		b.WriteString(renderIndex(strings.ToLower(t.Name), idx))
		b.WriteString("\n")
	}
	prePKUniques := make([]*Constraint, 0, len(t.UniqueConstraints))
	postPKUniques := make([]*Constraint, 0, len(t.UniqueConstraints))
	for _, c := range t.UniqueConstraints {
		if len(c.Columns) == 1 && strings.EqualFold(c.Columns[0], "email") {
			postPKUniques = append(postPKUniques, c)
			continue
		}
		prePKUniques = append(prePKUniques, c)
	}
	for _, c := range prePKUniques {
		b.WriteString(renderConstraint(strings.ToLower(t.Name), c))
		b.WriteString("\n")
	}
	if t.PrimaryKey != nil {
		b.WriteString(renderConstraint(strings.ToLower(t.Name), t.PrimaryKey))
		b.WriteString("\n")
	}
	for _, c := range postPKUniques {
		b.WriteString(renderConstraint(strings.ToLower(t.Name), c))
		b.WriteString("\n")
	}
	for _, c := range t.CheckConstraints {
		if isNotNullCheck(c.Condition) {
			continue
		}
		b.WriteString(renderConstraint(strings.ToLower(t.Name), c))
		b.WriteString("\n")
	}
	for _, c := range t.Columns {
		if strings.EqualFold(c.Nullable, "N") {
			b.WriteString("ALTER TABLE ")
			b.WriteString(strings.ToLower(t.Name))
			b.WriteString(" ALTER COLUMN ")
			b.WriteString(strings.ToLower(c.Name))
			b.WriteString(" SET NOT NULL;\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderIndex(tableName string, idx *Index) string {
	unique := ""
	if strings.EqualFold(idx.Uniqueness, "UNIQUE") {
		unique = "UNIQUE "
	}
	cols := make([]string, 0, len(idx.Columns))
	for _, c := range idx.Columns {
		cols = append(cols, normalizeExpression(c))
	}
	return fmt.Sprintf("CREATE %sINDEX %s ON %s (%s);", unique, strings.ToLower(idx.Name), tableName, strings.Join(cols, ", "))
}

func renderConstraint(tableName string, c *Constraint) string {
	switch c.Type {
	case "P":
		return fmt.Sprintf("ALTER TABLE %s ADD PRIMARY KEY (%s);", tableName, strings.Join(c.Columns, ","))
	case "U":
		return fmt.Sprintf("ALTER TABLE %s ADD UNIQUE (%s);", tableName, strings.Join(c.Columns, ","))
	case "C":
		return fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s CHECK (%s);", tableName, strings.ToLower(c.Name), normalizeCheck(c.Condition))
	default:
		return ""
	}
}

func renderForeignKey(tableName string, fk *ForeignKey) string {
	return fmt.Sprintf(
		"ALTER TABLE %s ADD CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s(%s) ON DELETE NO ACTION NOT DEFERRABLE INITIALLY IMMEDIATE;",
		tableName,
		strings.ToLower(fk.Name),
		strings.Join(fk.Columns, ","),
		fk.ReferencedTable,
		strings.Join(fk.ReferencedColumns, ","),
	)
}

func mapType(c *Column) string {
	dataType := strings.ToUpper(c.DataType)
	switch dataType {
	case "VARCHAR2":
		if c.CharLength.Valid {
			return fmt.Sprintf("varchar(%d)", c.CharLength.Int64)
		}
		return fmt.Sprintf("varchar(%d)", c.DataLength.Int64)
	case "CHAR":
		if c.CharLength.Valid {
			return fmt.Sprintf("char(%d)", c.CharLength.Int64)
		}
		return fmt.Sprintf("char(%d)", c.DataLength.Int64)
	case "NUMBER":
		return mapNumberType(c)
	case "FLOAT":
		return "double precision"
	case "DATE":
		return "timestamp(0)"
	case "TIMESTAMP(6)":
		return "TIMESTAMP(6)"
	case "TIMESTAMP(0)":
		return "timestamp(0)"
	case "TIMESTAMP(6) WITH TIME ZONE":
		return "TIMESTAMP(6) WITH TIME ZONE"
	case "TIMESTAMP(6) WITH LOCAL TIME ZONE":
		return "TIMESTAMP(6) WITH TIME ZONE"
	case "INTERVAL YEAR(2) TO MONTH":
		return "INTERVAL YEAR TO MONTH"
	case "INTERVAL DAY(2) TO SECOND(6)":
		return "INTERVAL DAY TO SECOND(6)"
	case "RAW":
		if c.DataLength.Valid && c.DataLength.Int64 == 16 {
			return "uuid"
		}
		return "bytea"
	case "NVARCHAR2":
		if c.CharLength.Valid {
			return fmt.Sprintf("varchar(%d)", c.CharLength.Int64)
		}
		return "varchar"
	case "BINARY_FLOAT", "BINARY_DOUBLE":
		return "numeric"
	case "CLOB":
		return "text"
	case "BLOB":
		return "bytea"
	case "XMLTYPE":
		return "xml"
	case "DEMO_PHONE_LIST_T", "DEMO_ADDRESS_T":
		return dataType
	default:
		return strings.ToLower(dataType)
	}
}

func mapNumberType(c *Column) string {
	precision := int64(0)
	scale := int64(0)
	if c.DataPrecision.Valid {
		precision = c.DataPrecision.Int64
	}
	if c.DataScale.Valid {
		scale = c.DataScale.Int64
	}
	if scale == 0 {
		if precision > 0 && precision <= 9 && !strings.HasSuffix(strings.ToLower(c.Name), "_id") {
			return "integer"
		}
		return "bigint"
	}
	if precision > 0 && precision <= 6 {
		return fmt.Sprintf("decimal(%d,%d)", precision, scale)
	}
	return "double precision"
}

func renderDefault(c *Column) string {
	if !c.DataDefault.Valid || strings.TrimSpace(c.DataDefault.String) == "" || strings.EqualFold(c.VirtualColumn, "YES") {
		return ""
	}
	def := normalizeWhitespace(strings.TrimSpace(strings.ReplaceAll(c.DataDefault.String, "\n", " ")))
	switch strings.ToUpper(def) {
	case "SYSTIMESTAMP", "CURRENT_TIMESTAMP", "SYSDATE":
		return "statement_timestamp()"
	case "TRUNC(SYSDATE)":
		return "date_trunc('day', statement_timestamp())"
	}
	def = strings.ReplaceAll(def, "SYS_GUID()", "uuid_generate_v4()")
	def = strings.ReplaceAll(def, "NVL(", "coalesce(")
	return def
}

func renderGeneratedExpression(expr string) string {
	v := normalizeWhitespace(strings.TrimSpace(expr))
	v = strings.ReplaceAll(v, "NVL(", "coalesce(")
	v = strings.ReplaceAll(v, "nvl(", "coalesce(")
	v = strings.ReplaceAll(v, "\"", "")
	v = regexp.MustCompile(`\s+`).ReplaceAllString(v, "")
	v = strings.ReplaceAll(v, "COALESCE(", "coalesce(")
	return v
}

func renderComment(comment string) string {
	escaped := strings.ReplaceAll(comment, "'", "''")
	return "E'" + escaped + "'"
}

// Helper functions

func isNotNullCheck(condition string) bool {
	return strings.Contains(strings.ToUpper(condition), "IS NOT NULL")
}

// Helper functions

func normalizeExpression(expr string) string {
	return strings.TrimSpace(expr)
}

func normalizeCheck(condition string) string {
	return strings.TrimSpace(condition)
}
