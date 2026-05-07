package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Programmable object rendering - keep Oracle internals with minimal conversion

// convertProcedure creates a PostgreSQL CREATE OR REPLACE PROCEDURE statement
func convertProcedure(p *Procedure) string {
	// Build parameter string (PG style: name type or name OUT type)
	paramParts := make([]string, 0, len(p.Params))
	for _, param := range p.Params {
		if param.InOut == "OUT" {
			paramParts = append(paramParts, fmt.Sprintf("%s OUT %s", param.Name, param.DataType))
		} else if param.InOut == "IN/OUT" {
			paramParts = append(paramParts, fmt.Sprintf("%s INOUT %s", param.Name, param.DataType))
		} else {
			paramParts = append(paramParts, fmt.Sprintf("%s %s", param.Name, param.DataType))
		}
	}

	var paramStr string
	if len(paramParts) == 0 {
		paramStr = ""
	} else {
		paramStr = " " + strings.Join(paramParts, ", ") + " "
	}

	schemaName := strings.ToLower(p.Owner)
	procName := strings.ToLower(p.Name)

	// Extract and minimally convert body
	body := extractProcBody(p.Body)
	body = minimalProcBodyConversion(body)

	var b strings.Builder
	b.WriteString("\n\n\n\n")
	b.WriteString("CREATE OR REPLACE PROCEDURE ")
	b.WriteString(schemaName)
	b.WriteString(".")
	b.WriteString(procName)
	b.WriteString(" (")
	b.WriteString(paramStr)
	b.WriteString(") AS $body$\n")
	b.WriteString(body)
	b.WriteString("\n$body$\nLANGUAGE PLPGSQL\nSECURITY DEFINER\n;\n")
	b.WriteString(fmt.Sprintf("-- REVOKE ALL ON PROCEDURE %s.%s (%s) FROM PUBLIC;\n\n\n",
		schemaName, procName, paramStr))
	return b.String()
}

// convertFunction creates a PostgreSQL CREATE OR REPLACE FUNCTION statement
func convertFunction(f *Function) string {
	// Build parameter string
	paramParts := make([]string, 0, len(f.Params))
	for _, p := range f.Params {
		part := p.Name + " " + p.DataType
		if p.Defaulted {
			part += " DEFAULT " + p.DefaultValue
		}
		paramParts = append(paramParts, part)
	}

	var paramStr string
	if len(paramParts) == 0 {
		paramStr = ""
	} else {
		hasDefault := false
		for _, p := range f.Params {
			if p.Defaulted {
				hasDefault = true
				break
			}
		}
		if hasDefault {
			paramStr = " " + strings.Join(paramParts, ", ")
		} else {
			paramStr = " " + strings.Join(paramParts, ", ") + " "
		}
	}

	schemaName := strings.ToLower(f.Owner)
	funcName := strings.ToLower(f.Name)

	// Extract and minimally convert body
	body := extractAndConvertFuncBody(f.RawSource)

	var b strings.Builder
	b.WriteString("\n\n\n\n")
	b.WriteString("CREATE OR REPLACE FUNCTION ")
	b.WriteString(schemaName)
	b.WriteString(".")
	b.WriteString(funcName)
	b.WriteString(" (")
	b.WriteString(paramStr)
	b.WriteString(") RETURNS ")
	b.WriteString(f.ReturnType)
	b.WriteString(" AS $body$\n")
	b.WriteString(body)
	b.WriteString("\n$body$\nLANGUAGE PLPGSQL\nSECURITY DEFINER\n;\n")
	b.WriteString(fmt.Sprintf("-- REVOKE ALL ON FUNCTION %s.%s (%s) FROM PUBLIC;\n\n\n",
		schemaName, funcName, paramStr))
	return b.String()
}

// extractProcBody extracts body from IS/AS to END;
func extractProcBody(source string) string {
	reIS := regexp.MustCompile(`(?is)\bIS\s*\n(.+)`)
	reAS := regexp.MustCompile(`(?is)\bAS\s*\n(.+)`)

	var body string
	if m := reIS.FindStringSubmatch(source); len(m) > 1 {
		body = m[1]
	} else if m := reAS.FindStringSubmatch(source); len(m) > 1 {
		body = m[1]
	} else {
		body = source
	}
	
	body = strings.TrimRight(body, "\n\r /")
	// Normalize END routineName; -> END; and BEGIN indentation
	body = regexp.MustCompile(`(?i)\bEND\s+\w+\s*;`).ReplaceAllString(body, "END;")
	body = regexp.MustCompile(`(?m)^\s+BEGIN\b`).ReplaceAllStringFunc(body, func(s string) string { return "BEGIN" })
	return body
}

// minimalProcBodyConversion does minimal Oracle to PostgreSQL conversion
// Keeps the logic and syntax as-is from Oracle, only fixing what's incompatible
func minimalProcBodyConversion(body string) string {
	// Remove space before ( in INSERT INTO table ( -> INSERT INTO table(
	body = regexp.MustCompile(`(?i)(INSERT\s+INTO\s+\w+)\s+(\()`).ReplaceAllString(body, "$1$2")
	
	// Add CALL before package.procedure( calls (but not already with CALL)
	re := regexp.MustCompile(`(?m)(\s+)([a-zA-Z_][a-zA-Z0-9_]*\.[a-zA-Z_][a-zA-Z0-9_]*\s*\()`)
	body = re.ReplaceAllStringFunc(body, func(match string) string {
		if !strings.Contains(match, "CALL") {
			// Preserve the leading whitespace and add CALL
			parts := strings.SplitN(match, "[a-zA-Z_]", 2)
			if len(parts) > 0 {
				return strings.TrimPrefix(match, "CALL ") // Remove if already there
			}
		}
		return match
	})
	
	return body
}

// extractAndConvertFuncBody extracts function body with minimal conversion
func extractAndConvertFuncBody(rawSource string) string {
	// Find IS or AS on its own after the function header
	reISAS := regexp.MustCompile(`(?im)^\s*(IS|AS)\s*$`)
	loc := reISAS.FindStringIndex(rawSource)
	if loc == nil {
		return rawSource
	}
	
	afterISAS := rawSource[loc[1]:]
	afterISAS = strings.TrimLeft(afterISAS, "\n")

	// Split at BEGIN
	reBegin := regexp.MustCompile(`(?im)^\s*BEGIN\b`)
	beginLoc := reBegin.FindStringIndex(afterISAS)
	if beginLoc == nil {
		return afterISAS
	}

	declSection := afterISAS[:beginLoc[0]]
	bodySection := "BEGIN" + afterISAS[beginLoc[1]:]

	// Minimal conversion: only fix syntax incompatibilities, keep logic as-is
	declTrimmed := strings.TrimSpace(declSection)
	if declTrimmed == "" {
		// No DECLARE section
		return bodySection
	}
	
	return "DECLARE\n" + declSection + "\n" + bodySection
}

// renderFunctions creates the export output for functions
func renderFunctions(funcs []*Function) string {
	var out strings.Builder
	for _, f := range funcs {
		out.WriteString(convertFunction(f))
	}
	return out.String()
}

// renderProcedures creates the export output for procedures
func renderProcedures(procs []*Procedure) string {
	var out strings.Builder
	for _, p := range procs {
		out.WriteString(convertProcedure(p))
	}
	return out.String()
}

// Database loading functions for PROCEDURE and FUNCTION are in export_procedure_function_loading.go
