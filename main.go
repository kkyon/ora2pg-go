package main

import (
	"bufio"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "github.com/sijms/go-ora/v2"
)

func main() {
	typeName := flag.String("type", "TABLE", "Export type: TABLE, TYPE, SEQUENCE, VIEW, MVIEW, PROCEDURE, FUNCTION, PACKAGE")
	configPath := flag.String("config", "ora2pg.conf", "Path to ora2pg.conf configuration file")
	oracleHost := flag.String("oracle-host", "localhost", "Oracle hostname")
	outPath := flag.String("out", "", "Output file path")
	flag.Parse()

	if *outPath == "" {
		fatalf("--out parameter is required")
	}

	// Load configuration
	config, err := loadConfig(*configPath)
	if err != nil {
		fatalf("load config: %v", err)
	}

	// Override Oracle host from flag (default is "localhost")
	if *oracleHost != "" {
		// Rebuild the DSN with the specified host
		config.OracleDSN = fmt.Sprintf("host=%s;port=1521;service_name=XEPDB1", *oracleHost)
	}

	// Connect to Oracle
	db, err := connectOracle(config)
	if err != nil {
		fatalf("connect oracle: %v", err)
	}
	defer db.Close()

	// Export based on type
	var output string
	switch strings.ToUpper(*typeName) {
	case "TABLE":
		tables, err := loadTables(db)
		if err != nil {
			fatalf("load tables: %v", err)
		}
		output = renderTables(tables)

	case "TYPE":
		types, err := loadTypes(db)
		if err != nil {
			fatalf("load types: %v", err)
		}
		output = renderTypes(types)

	case "SEQUENCE":
		seqs, err := loadSequences(db)
		if err != nil {
			fatalf("load sequences: %v", err)
		}
		output = renderSequences(seqs)

	case "VIEW":
		views, err := loadViews(db)
		if err != nil {
			fatalf("load views: %v", err)
		}
		output = renderViews(views)

	case "MVIEW":
		mviews, err := loadMviews(db)
		if err != nil {
			fatalf("load mviews: %v", err)
		}
		output = renderMviews(mviews)

	case "PROCEDURE":
		procs, err := loadProcedures(db)
		if err != nil {
			fatalf("load procedures: %v", err)
		}
		output = renderProcedures(procs)

	case "FUNCTION":
		funcs, err := loadFunctions(db)
		if err != nil {
			fatalf("load functions: %v", err)
		}
		output = renderFunctions(funcs)

	case "PACKAGE":
		pkgs, err := loadPackages(db)
		if err != nil {
			fatalf("load packages: %v", err)
		}
		output = renderPackages(pkgs, config.PackageAsSchema)

	default:
		fatalf("unsupported export type: %s", *typeName)
	}

	// Write output
	if err := os.MkdirAll(filepath.Dir(*outPath), 0o755); err != nil {
		fatalf("create output directory: %v", err)
	}
	if err := os.WriteFile(*outPath, []byte(output), 0o644); err != nil {
		fatalf("write output: %v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

// Configuration loading

func loadConfig(path string) (Config, error) {
	config := Config{
		OracleUser:      "app",
		OraclePwd:       "app",
		OracleDSN:       "host=oracle;port=1521;service_name=XEPDB1",
		PackageAsSchema: true, // Default: use schema per package
	}

	file, err := os.Open(path)
	if err != nil {
		return config, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}

		parts := strings.SplitN(line, " ", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.ToLower(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "oracle_user":
			config.OracleUser = value
		case "oracle_pwd":
			config.OraclePwd = value
		case "oracle_dsn":
			// Parse Perl DBI format to Go Oracle driver format
			// Input: dbi:Oracle:host=oracle;port=1521;service_name=XEPDB1
			// Output: host=oracle;port=1521;service_name=XEPDB1
			if strings.Contains(value, "dbi:Oracle:") {
				value = strings.TrimPrefix(value, "dbi:Oracle:")
			}
			config.OracleDSN = value
		case "schema":
			config.Schema = value
		case "package_as_schema":
			// Default true; set false with "0" or "false"
			config.PackageAsSchema = strings.ToLower(value) != "0" && strings.ToLower(value) != "false"
		}
	}

	// Set default package_as_schema if not explicitly set
	// (already true by struct initialization, but explicit for clarity)
	return config, scanner.Err()
}

// Database connection

func connectOracle(config Config) (*sql.DB, error) {
	// Parse the Perl DBI format DSN into connection components
	// Format: dbi:Oracle:host=oracle;port=1521;service_name=XEPDB1
	dsn := config.OracleDSN
	if strings.Contains(dsn, "dbi:Oracle:") {
		dsn = strings.TrimPrefix(dsn, "dbi:Oracle:")
	}

	host := "localhost"
	port := 1521
	service := ""

	// Parse key=value pairs from DSN
	parts := strings.Split(dsn, ";")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(kv[0]))
		value := strings.TrimSpace(kv[1])
		switch key {
		case "host":
			host = value
		case "port":
			p, err := strconv.Atoi(value)
			if err == nil {
				port = p
			}
		case "service_name":
			service = value
		}
	}

	// Build Go Oracle driver DSN format
	// oracle://user:password@host:port/service_name
	connectionString := fmt.Sprintf("oracle://%s:%s@%s:%d/%s", 
		config.OracleUser, config.OraclePwd, host, port, service)
	
	return sql.Open("oracle", connectionString)
}
