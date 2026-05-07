package main

import (
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Package export with minimal programmable object conversion

// loadPackages loads all packages from the database
func loadPackages(db *sql.DB) ([]*Package, error) {
	rows, err := db.Query("SELECT DISTINCT name FROM user_source WHERE type = 'PACKAGE' ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pkgs []*Package
	for rows.Next() {
		var pkgName string
		if err := rows.Scan(&pkgName); err != nil {
			return nil, err
		}

		pkg := &Package{
			Name:  pkgName,
			Owner: "app",
		}

		if err := parsePackageSpec(db, pkgName, pkg); err != nil {
			fmt.Fprintf(os.Stderr, "WARN: parsePackageSpec %s: %v\n", pkgName, err)
			continue
		}

		pkgs = append(pkgs, pkg)
	}

	return pkgs, rows.Err()
}

// parsePackageSpec parses package specification and body from user_source
func parsePackageSpec(db *sql.DB, pkgName string, pkg *Package) error {
	// Query both PACKAGE spec and PACKAGE BODY source
	rows, err := db.Query("SELECT text FROM user_source WHERE name = :1 AND type IN ('PACKAGE', 'PACKAGE BODY') ORDER BY type DESC, line", strings.ToUpper(pkgName))
	if err != nil {
		return err
	}
	defer rows.Close()

	var source strings.Builder
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return err
		}
		source.WriteString(text)
		source.WriteString("\n")
	}

	if err := rows.Err(); err != nil {
		return err
	}

	spec := source.String()
	if spec == "" {
		return nil
	}

	// Parse for SUBTYPE declarations (SUBTYPE name IS basetype;)
	subtypeRe := regexp.MustCompile(`(?i)SUBTYPE\s+(\w+)\s+IS\s+(\w+(?:\s*\(\s*\d+\s*\))?)\s*;`)
	for _, match := range subtypeRe.FindAllStringSubmatch(spec, -1) {
		domain := &Domain{
			Name:     match[1],
			BaseType: mapOracleToPgTypeWithLength(match[2]),
		}
		pkg.Domains = append(pkg.Domains, domain)
	}

	// Parse for TYPE declarations (TYPE name IS ...)
	typeRe := regexp.MustCompile(`(?i)TYPE\s+(\w+)\s+IS\s+((?:[^;]|\n)+?);`)
	for _, match := range typeRe.FindAllStringSubmatch(spec, -1) {
		pkgType := &PackageType{
			Name:       match[1],
			Definition: strings.TrimSpace(match[2]),
		}

		// Determine type kind
		typeDefUpper := strings.ToUpper(pkgType.Definition)
		if strings.Contains(typeDefUpper, "RECORD") {
			pkgType.TypeKind = "RECORD"
		} else if strings.Contains(typeDefUpper, "REF CURSOR") {
			pkgType.TypeKind = "REFCURSOR"
		} else {
			pkgType.TypeKind = "OTHER"
		}

		pkg.Types = append(pkg.Types, pkgType)
	}

	// Extract function and procedure definitions ordered by spec declaration order
	argRows, err := db.Query(`
SELECT up.procedure_name
FROM user_procedures up
WHERE up.object_name = :1
  AND up.procedure_name IS NOT NULL
ORDER BY up.subprogram_id`, strings.ToUpper(pkgName))
	if err != nil {
		return err
	}
	defer argRows.Close()

	seen := map[string]bool{}
	for argRows.Next() {
		var objName string
		if err := argRows.Scan(&objName); err != nil {
			return err
		}
		if seen[objName] {
			continue
		}
		seen[objName] = true

		// Determine if it's a function (has return type at position=0) or procedure
		var retCount int
		retRows, err := db.Query(`SELECT COUNT(*) FROM user_arguments WHERE package_name = :1 AND object_name = :2 AND position = 0 AND argument_name IS NULL`, strings.ToUpper(pkgName), objName)
		if err != nil {
			return err
		}
		if retRows.Next() {
			retRows.Scan(&retCount)
		}
		retRows.Close()

		isFunction := retCount > 0

		renderedName := strings.ToLower(pkgName) + "_" + strings.ToLower(objName)

		if isFunction {
			retType, err := loadFuncReturnTypeFromPkg(db, pkgName, objName)
			if err != nil {
				retType = "void"
			}
			// If Oracle returns REF CURSOR, try to find the actual named cursor type from package spec
			if strings.EqualFold(retType, "ref cursor") {
				if named := extractNamedReturnTypeFromSpec(spec, objName); named != "" {
					retType = named
				}
			}
			params, err := loadFuncParamsFromPkg(db, pkgName, objName)
			if err != nil {
				return err
			}
			rawSource, err := loadFuncSourceFromPkg(db, pkgName, objName)
			if err != nil {
				return err
			}
			fn := &Function{
				Name:       renderedName,
				Owner:      "app",
				Params:     params,
				ReturnType: retType,
				RawSource:  rawSource,
			}
			pkg.Functions = append(pkg.Functions, fn)
		} else {
			// PROCEDURE
			procParams, err := loadProcParamsFromPkg(db, pkgName, objName)
			if err != nil {
				return err
			}
			body, err := loadProcBodyFromPkg(db, pkgName, objName)
			if err != nil {
				return err
			}
			proc := &Procedure{
				Name:   renderedName,
				Owner:  "app",
				Params: procParams,
				Body:   body,
			}
			pkg.Procedures = append(pkg.Procedures, proc)
		}
	}

	return argRows.Err()
}

// Helper functions for loading procedure/function metadata

func loadFuncReturnTypeFromPkg(db *sql.DB, pkgName, funcName string) (string, error) {
	rows, err := db.Query(`
SELECT data_type
FROM user_arguments
WHERE package_name = :1
  AND object_name = :2
  AND position = 0
  AND argument_name IS NULL`, strings.ToUpper(pkgName), strings.ToUpper(funcName))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if rows.Next() {
		var dataType string
		if err := rows.Scan(&dataType); err != nil {
			return "", err
		}
		return mapFuncReturnType(dataType), nil
	}
	return "void", nil
}

func loadFuncParamsFromPkg(db *sql.DB, pkgName, funcName string) ([]*FuncParam, error) {
	rows, err := db.Query(`
SELECT argument_name, position, data_type, in_out, data_precision, data_scale, data_length, defaulted
FROM user_arguments
WHERE package_name = :1
  AND object_name = :2
  AND position > 0
  AND argument_name IS NOT NULL
ORDER BY position`, strings.ToUpper(pkgName), strings.ToUpper(funcName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	params := make([]*FuncParam, 0)
	for rows.Next() {
		var name, dataType, inOut, defaulted string
		var position int
		var precision, scale, length sql.NullInt64
		if err := rows.Scan(&name, &position, &dataType, &inOut, &precision, &scale, &length, &defaulted); err != nil {
			return nil, err
		}
		pgType := mapOracleToPgTypeWithLength(dataType)
		params = append(params, &FuncParam{
			Name:         strings.ToLower(name),
			DataType:     pgType,
			InOut:        inOut,
			Position:     position,
			Defaulted:    defaulted == "Y",
		})
	}
	return params, rows.Err()
}

func loadFuncSourceFromPkg(db *sql.DB, pkgName, funcName string) (string, error) {
	// Load the PACKAGE BODY source and extract the function
	rows, err := db.Query(`
SELECT text FROM user_source
WHERE name = :1 AND type = 'PACKAGE BODY'
ORDER BY line`, strings.ToUpper(pkgName))
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var sb strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", err
		}
		sb.WriteString(line)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return extractRoutineFromPkgBody(sb.String(), "FUNCTION", funcName), nil
}

func loadProcParamsFromPkg(db *sql.DB, pkgName, procName string) ([]*ProcParam, error) {
	rows, err := db.Query(`
SELECT argument_name, position, data_type, in_out
FROM user_arguments
WHERE package_name = :1
  AND object_name = :2
  AND position > 0
  AND argument_name IS NOT NULL
ORDER BY position`, strings.ToUpper(pkgName), strings.ToUpper(procName))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	params := make([]*ProcParam, 0)
	for rows.Next() {
		var name, dataType, inOut string
		var position int
		if err := rows.Scan(&name, &position, &dataType, &inOut); err != nil {
			return nil, err
		}
		pgType := mapOracleToPgTypeWithLength(dataType)
		params = append(params, &ProcParam{
			Name:     strings.ToLower(name),
			DataType: pgType,
			InOut:    inOut,
			Position: position,
		})
	}
	return params, rows.Err()
}

func loadProcBodyFromPkg(db *sql.DB, pkgName, procName string) (string, error) {
	// Load PACKAGE BODY and extract procedure
	rows, err := db.Query(`
SELECT text FROM user_source
WHERE name = :1 AND type = 'PACKAGE BODY'
ORDER BY line`, strings.ToUpper(pkgName))
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var sb strings.Builder
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return "", err
		}
		sb.WriteString(line)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return extractRoutineFromPkgBody(sb.String(), "PROCEDURE", procName), nil
}

// Helper functions

func extractNamedReturnTypeFromSpec(spec, funcName string) string {
	re := regexp.MustCompile(`(?i)FUNCTION\s+` + regexp.QuoteMeta(funcName) + `\s*(?:\([^)]*\))?\s+RETURN\s+(\w+)`)
	m := re.FindStringSubmatch(spec)
	if len(m) >= 2 {
		return strings.ToUpper(m[1])
	}
	return ""
}

func extractRoutineFromPkgBody(pkgBody, routineType, routineName string) string {
	// Find FUNCTION/PROCEDURE name, handling optional parentheses and RETURN clause
	pattern := fmt.Sprintf(`(?is)%s\s+%s\s*[\(\s]`, regexp.QuoteMeta(routineType), regexp.QuoteMeta(routineName))
	re := regexp.MustCompile(pattern)
	loc := re.FindStringIndex(pkgBody)
	if loc == nil {
		return ""
	}

	body := pkgBody[loc[0]:]
	// Find END routineName;
	endPattern := regexp.MustCompile(`(?i)END\s+` + regexp.QuoteMeta(routineName) + `\s*;`)
	endLoc := endPattern.FindStringIndex(body)
	if endLoc == nil {
		return body
	}
	return body[:endLoc[1]]
}

// qualifyIntraPackageCalls adds package prefix to intra-package routine calls
func qualifyIntraPackageCalls(body, shortName, pkgPrefix string) string {
	// Only replace shortname when it appears as a standalone identifier
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(shortName) + `\b`)
	return re.ReplaceAllStringFunc(body, func(match string) string {
		return pkgPrefix + match
	})
}

// renderPackages creates the export output for packages
func renderPackages(pkgs []*Package) string {
	var output strings.Builder

	for _, pkg := range pkgs {
		// Build set of local type names (domains and named types)
		localTypeNames := map[string]bool{}
		for _, d := range pkg.Domains {
			localTypeNames[strings.ToLower(d.Name)] = true
		}
		for _, t := range pkg.Types {
			localTypeNames[strings.ToLower(t.Name)] = true
		}

		// Add package comment with leading blank lines
		output.WriteString(fmt.Sprintf("\n\n-- Oracle package '%s' declaration, please edit to match PostgreSQL syntax.\n", pkg.Name))

		// Render DOMAIN declarations
		for _, domain := range pkg.Domains {
			output.WriteString(fmt.Sprintf("CREATE DOMAIN %s.%s AS %s;\n",
				strings.ToLower(pkg.Owner),
				strings.ToLower(domain.Name),
				domain.BaseType))
		}

		// Render TYPE declarations
		for _, pkgType := range pkg.Types {
			switch pkgType.TypeKind {
			case "RECORD":
				recordDef := normalizeRecordDefinitionWithPkg(pkgType.Definition, strings.ToLower(pkg.Owner), localTypeNames)
				output.WriteString(fmt.Sprintf("CREATE TYPE %s.%s AS (\n%s\n);\n",
					strings.ToLower(pkg.Owner),
					strings.ToLower(pkgType.Name),
					recordDef))
				output.WriteString("\n")
			case "REFCURSOR":
				output.WriteString("-- Unsupported, please edit to match PostgreSQL syntax\n")
				output.WriteString(fmt.Sprintf("CREATE OR REPLACE TYPE %s.%s AS REFCURSOR RETURN %s;\n\n\n\n\n\n\n",
					strings.ToLower(pkg.Owner),
					strings.ToLower(pkgType.Name),
					extractRefCursorReturnType(pkgType.Definition)))
			}
		}

		// Build list of intra-package routine names for qualification
		pkgPrefix := strings.ToLower(pkg.Name) + "_"
		intraPackageNames := []string{}
		for _, fn := range pkg.Functions {
			shortName := strings.TrimPrefix(strings.ToLower(fn.Name), pkgPrefix)
			intraPackageNames = append(intraPackageNames, shortName)
		}
		for _, proc := range pkg.Procedures {
			shortName := strings.TrimPrefix(strings.ToLower(proc.Name), pkgPrefix)
			intraPackageNames = append(intraPackageNames, shortName)
		}

		// Render FUNCTION definitions
		firstRoutine := true
		for _, fn := range pkg.Functions {
			fnStr := strings.TrimLeft(convertFunction(fn), "\n")
			fnStr = strings.TrimRight(fnStr, "\n")
			// Qualify intra-package calls
			for _, shortName := range intraPackageNames {
				fnStr = qualifyIntraPackageCalls(fnStr, shortName, pkgPrefix)
			}
			if firstRoutine {
				firstRoutine = false
			} else {
				output.WriteString("\n\n\n")
			}
			output.WriteString(fnStr)
			output.WriteString("\n")
		}

		// Render PROCEDURE definitions
		for _, proc := range pkg.Procedures {
			procStr := strings.TrimLeft(convertProcedure(proc), "\n")
			procStr = strings.TrimRight(procStr, "\n")
			// Qualify intra-package calls
			for _, shortName := range intraPackageNames {
				procStr = qualifyIntraPackageCalls(procStr, shortName, pkgPrefix)
			}
			output.WriteString("\n\n\n")
			output.WriteString(procStr)
			output.WriteString("\n")
		}

		// Trim trailing blank lines before closing comment
		trimmed := strings.TrimRight(output.String(), "\n")
		output.Reset()
		output.WriteString(trimmed)
		output.WriteString("\n")

		// Add closing comment
		output.WriteString(fmt.Sprintf("-- End of Oracle package '%s' declaration\n\n", pkg.Name))
	}

	return output.String()
}

// normalizeRecordDefinition formats RECORD field definitions
func normalizeRecordDefinition(def string) string {
	return normalizeRecordDefinitionWithPkg(def, "", nil)
}

func normalizeRecordDefinitionWithPkg(def, pkgNamespace string, localTypeNames map[string]bool) string {
	// Remove leading parenthesis and split fields
	def = strings.Trim(def, "() \t\n")

	// Parse fields with support for nested parentheses
	var fields []string
	var current string
	depth := 0
	for _, ch := range def {
		switch ch {
		case '(':
			depth++
			current += string(ch)
		case ')':
			depth--
			current += string(ch)
		case ',':
			if depth == 0 {
				fields = append(fields, strings.TrimSpace(current))
				current = ""
			} else {
				current += string(ch)
			}
		default:
			current += string(ch)
		}
	}
	if strings.TrimSpace(current) != "" {
		fields = append(fields, strings.TrimSpace(current))
	}

	// Parse field names and types, build output with alignment
	maxNameLen := 0
	type fieldDef struct{ name, typ string }
	var parsed []fieldDef
	for _, field := range fields {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		parts := strings.Fields(field)
		if len(parts) >= 2 {
			name := strings.ToLower(parts[0])
			rawType := strings.Join(parts[1:], " ")
			pgType := mapRecordFieldType(rawType, pkgNamespace, localTypeNames)
			parsed = append(parsed, fieldDef{name, pgType})
			if len(name) > maxNameLen {
				maxNameLen = len(name)
			}
		}
	}

	if len(parsed) == 0 {
		return def
	}

	// Format with column alignment like Perl
	var lines []string
	for _, f := range parsed {
		pad := strings.Repeat(" ", maxNameLen-len(f.name)+1)
		lines = append(lines, f.name+pad+f.typ)
	}

	if len(lines) == 1 {
		return lines[0] + "\n\t"
	}

	// Multiple lines: first with comma, middle with commas, last without
	result := lines[0] + ","
	if len(lines) > 2 {
		for _, line := range lines[1 : len(lines)-1] {
			result += "\n\t\t" + line + ","
		}
	}
	result += "\n\t\t" + lines[len(lines)-1] + "\n\t"
	return result
}

// extractRefCursorReturnType parses the return type from REF CURSOR definition
func extractRefCursorReturnType(def string) string {
	refRe := regexp.MustCompile(`(?i)REF\s+CURSOR\s+RETURN\s+(\w+)`)
	matches := refRe.FindStringSubmatch(def)
	if len(matches) >= 2 {
		return matches[1]
	}
	return "UNKNOWN"
}
