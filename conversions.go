package main

import (
	"database/sql"
	"regexp"
	"strconv"
	"strings"
)

// Type conversion functions for different export types

// mapOracleToPgType maps basic Oracle types to PostgreSQL
func mapOracleToPgType(dataType string) string {
	switch strings.ToUpper(strings.TrimSpace(dataType)) {
	case "NUMBER", "INTEGER":
		return "bigint"
	case "VARCHAR2", "NVARCHAR2", "VARCHAR":
		return "varchar"
	case "CHAR":
		return "char"
	case "DATE":
		return "timestamp(0)"
	case "TIMESTAMP":
		return "timestamp"
	case "CLOB", "NCLOB":
		return "text"
	case "BLOB":
		return "bytea"
	case "BOOLEAN":
		return "boolean"
	}
	if strings.HasPrefix(strings.ToUpper(dataType), "TIMESTAMP") {
		return "timestamp"
	}
	return strings.ToLower(dataType)
}

// mapOracleToPgTypeWithLength maps Oracle types with lengths to PostgreSQL
func mapOracleToPgTypeWithLength(dataType string) string {
	dataType = strings.TrimSpace(dataType)

	// VARCHAR2(n)
	if re := regexp.MustCompile(`(?i)^VARCHAR2\s*\(\s*(\d+)\s*\)`); re.MatchString(dataType) {
		matches := re.FindStringSubmatch(dataType)
		if len(matches) >= 2 {
			return "varchar(" + matches[1] + ")"
		}
	}

	// CHAR(n)
	if re := regexp.MustCompile(`(?i)^CHAR\s*\(\s*(\d+)\s*\)`); re.MatchString(dataType) {
		matches := re.FindStringSubmatch(dataType)
		if len(matches) >= 2 {
			return "char(" + matches[1] + ")"
		}
	}

	// NUMBER(p,s) or NUMBER(p)
	if re := regexp.MustCompile(`(?i)^NUMBER\s*\(\s*(\d+)\s*,?\s*(\d+)?\s*\)`); re.MatchString(dataType) {
		matches := re.FindStringSubmatch(dataType)
		if len(matches) >= 3 && matches[2] != "" {
			return "numeric(" + matches[1] + "," + matches[2] + ")"
		}
		return "bigint"
	}

	return mapOracleToPgType(dataType)
}

// mapProcParamType maps Oracle parameter types to PostgreSQL
func mapProcParamType(dataType string, precision, scale, length sql.NullInt64) string {
	baseType := mapOracleToPgTypeWithLength(dataType)

	// Handle precision/scale if provided
	if strings.HasPrefix(baseType, "numeric") {
		return baseType
	}

	// For VARCHAR, apply length if available
	if strings.HasPrefix(baseType, "varchar") && length.Valid && length.Int64 > 0 {
		return "varchar(" + strconv.FormatInt(length.Int64, 10) + ")"
	}

	return baseType
}

// mapFuncReturnType maps Oracle function return types to PostgreSQL
func mapFuncReturnType(dataType string) string {
	return mapOracleToPgType(dataType)
}

// mapRecordFieldType maps record field types with package namespace support
func mapRecordFieldType(rawType, pkgNamespace string, localTypeNames map[string]bool) string {
	// Check if it's a known Oracle base type
	pgType := mapOracleToPgTypeWithLength(rawType)
	
	// Special case: NUMBER with no params -> bigint
	if regexp.MustCompile(`(?i)^NUMBER$`).MatchString(strings.TrimSpace(rawType)) {
		return "bigint"
	}
	
	// Check if it's a package-local type reference
	if pkgNamespace != "" && localTypeNames != nil {
		rawTypeLower := strings.ToLower(strings.Fields(rawType)[0])
		if localTypeNames[rawTypeLower] {
			return pkgNamespace + "." + rawTypeLower
		}
	}

	return pgType
}
// normalizeWhitespace collapses multiple whitespace into single spaces
func normalizeWhitespace(s string) string {
return regexp.MustCompile(`\s+`).ReplaceAllString(s, " ")
}
