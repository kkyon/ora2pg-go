package main

import (
	"database/sql"
	"regexp"
	"sort"
	"strings"
)

// Database loading functions for PROCEDURE and FUNCTION

// loadProcedures loads all PROCEDURE objects from Oracle
func loadProcedures(db *sql.DB) ([]*Procedure, error) {
	rows, err := db.Query(`
SELECT object_name
FROM user_objects
WHERE object_type = 'PROCEDURE'
  AND status = 'VALID'
ORDER BY object_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	procs := make([]*Procedure, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		params, err := loadProcParams(db, name)
		if err != nil {
			return nil, err
		}
		body, err := loadProcBody(db, name)
		if err != nil {
			return nil, err
		}
		procs = append(procs, &Procedure{
			Name:   name,
			Owner:  "app",
			Params: params,
			Body:   body,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(procs, func(i, j int) bool { return procs[i].Name < procs[j].Name })
	return procs, nil
}

// loadProcParams loads parameters for a procedure
func loadProcParams(db *sql.DB, procName string) ([]*ProcParam, error) {
	rows, err := db.Query(`
SELECT argument_name, position, data_type, in_out
FROM user_arguments
WHERE object_name = :1
  AND position > 0
  AND argument_name IS NOT NULL
ORDER BY position`, procName)
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
		pgType := mapProcParamType(dataType, sql.NullInt64{}, sql.NullInt64{}, sql.NullInt64{})
		params = append(params, &ProcParam{
			Name:     strings.ToLower(name),
			DataType: pgType,
			InOut:    inOut,
			Position: position,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return params, nil
}

// loadProcBody loads the source code for a procedure
func loadProcBody(db *sql.DB, procName string) (string, error) {
	rows, err := db.Query(`
SELECT text
FROM user_source
WHERE name = :1
  AND type = 'PROCEDURE'
ORDER BY line`, procName)
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
	return sb.String(), nil
}

// loadFunctions loads all FUNCTION objects from Oracle
func loadFunctions(db *sql.DB) ([]*Function, error) {
	rows, err := db.Query(`
SELECT object_name
FROM user_objects
WHERE object_type = 'FUNCTION'
  AND status = 'VALID'
ORDER BY object_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	funcs := make([]*Function, 0)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		retType, err := loadFuncReturnType(db, name)
		if err != nil {
			return nil, err
		}
		params, err := loadFuncParams(db, name)
		if err != nil {
			return nil, err
		}
		rawSource, err := loadFuncSource(db, name)
		if err != nil {
			return nil, err
		}
		// Parse default values from source header
		defaults := parseFuncDefaults(rawSource)
		for _, p := range params {
			if val, ok := defaults[p.Name]; ok {
				p.Defaulted = true
				p.DefaultValue = val
			}
		}
		funcs = append(funcs, &Function{
			Name:       name,
			Owner:      "app",
			Params:     params,
			ReturnType: retType,
			RawSource:  rawSource,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(funcs, func(i, j int) bool { return funcs[i].Name < funcs[j].Name })
	return funcs, nil
}

// loadFuncReturnType loads the return type for a function
func loadFuncReturnType(db *sql.DB, funcName string) (string, error) {
	rows, err := db.Query(`
SELECT data_type
FROM user_arguments
WHERE object_name = :1
  AND position = 0
  AND argument_name IS NULL`, funcName)
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

// loadFuncParams loads parameters for a function
func loadFuncParams(db *sql.DB, funcName string) ([]*FuncParam, error) {
	rows, err := db.Query(`
SELECT argument_name, position, data_type, in_out, data_precision, data_scale, data_length, defaulted
FROM user_arguments
WHERE object_name = :1
  AND position > 0
  AND argument_name IS NOT NULL
ORDER BY position`, funcName)
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
		pgType := mapProcParamType(dataType, precision, scale, length)
		params = append(params, &FuncParam{
			Name:      strings.ToLower(name),
			DataType:  pgType,
			InOut:     inOut,
			Position:  position,
			Defaulted: defaulted == "Y",
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return params, nil
}

// loadFuncSource loads the source code for a function
func loadFuncSource(db *sql.DB, funcName string) (string, error) {
	rows, err := db.Query(`
SELECT text
FROM user_source
WHERE name = :1
  AND type = 'FUNCTION'
ORDER BY line`, funcName)
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
	return sb.String(), nil
}

// parseFuncDefaults parses DEFAULT values from function source
func parseFuncDefaults(rawSource string) map[string]string {
	defaults := make(map[string]string)
	// Match "paramname TYPE ... DEFAULT value" in the header (before IS/AS)
	re := regexp.MustCompile(`(?i)\b(\w+)\s+(?:NUMBER|VARCHAR2?|DATE|CHAR|CLOB|BLOB|INTEGER)\s*(?:\(\s*\d+(?:\s*,\s*\d+)?\s*\))?\s+DEFAULT\s+(\S+)`)
	for _, m := range re.FindAllStringSubmatch(rawSource, -1) {
		if len(m) >= 3 {
			defaults[strings.ToLower(m[1])] = m[2]
		}
	}
	return defaults
}
