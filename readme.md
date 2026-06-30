# ora2pg-go

Go port of Ora2Pg export behavior.

## Overview

`ora2pg-go` is a Go implementation of selected Ora2Pg export flows.
This codebase is intended to be published as an independent public repository,
while the full migration workspace can remain private.

This project is a behavior-focused port from a Perl-based Ora2Pg workflow.
Implementation is metadata-driven and does not copy Perl source code.

> Porting in this repository was completed with GPT-5.3-Codex using VS Code and GitHub Copilot.

## Current Support

Export types currently supported by the CLI:

- TABLE
- TYPE
- SEQUENCE
- VIEW
- MVIEW
- PROCEDURE
- FUNCTION
- PACKAGE
- TRIGGER
- SYNONYM
- SHOW_REPORT

Some export types are still partial and may require manual review of generated SQL.

## Requirements

- Go 1.18+
- Oracle-accessible environment for metadata extraction
- A valid local config file (`./ora2pg.conf` is included)

## Build

From the `target/ora2pg-go` directory:

```bash
go mod tidy
go build -o ora2pg-go .
```

Binary output:

- `./ora2pg-go`

## Build with Make

A `Makefile` is included for common tasks.

From `target/ora2pg-go`:

```bash
make tidy
make build
```

Quick run demo via make:

```bash
make run-table
```

Available make targets:

- `make tidy`
- `make build`
- `make run-table`
- `make clean`

## Basic Usage

```bash
./ora2pg-go --config ./ora2pg.conf --type TABLE --oracle-host localhost --out ./output/ora2pg-go-TABLE.sql
```

## Feature Demo Commands

Run from `target/ora2pg-go`:

```bash
./ora2pg-go --config ./ora2pg.conf --type TABLE --oracle-host localhost --out ./output/ora2pg-go-TABLE.sql
./ora2pg-go --config ./ora2pg.conf --type TYPE --oracle-host localhost --out ./output/ora2pg-go-TYPE.sql
./ora2pg-go --config ./ora2pg.conf --type SEQUENCE --oracle-host localhost --out ./output/ora2pg-go-SEQUENCE.sql
./ora2pg-go --config ./ora2pg.conf --type VIEW --oracle-host localhost --out ./output/ora2pg-go-VIEW.sql
./ora2pg-go --config ./ora2pg.conf --type MVIEW --oracle-host localhost --out ./output/ora2pg-go-MVIEW.sql
./ora2pg-go --config ./ora2pg.conf --type PROCEDURE --oracle-host localhost --out ./output/ora2pg-go-PROCEDURE.sql
./ora2pg-go --config ./ora2pg.conf --type FUNCTION --oracle-host localhost --out ./output/ora2pg-go-FUNCTION.sql
./ora2pg-go --config ./ora2pg.conf --type PACKAGE --oracle-host localhost --out ./output/ora2pg-go-PACKAGE.sql
./ora2pg-go --config ./ora2pg.conf --type TRIGGER --oracle-host localhost --out ./output/ora2pg-go-TRIGGER.sql
./ora2pg-go --config ./ora2pg.conf --type SYNONYM --oracle-host localhost --out ./output/ora2pg-go-SYNONYM.sql
./ora2pg-go --config ./ora2pg.conf --type SHOW_REPORT --oracle-host localhost --out ./output/ora2pg-go-report.md
```

## Publishing Model

No GitHub Actions are required for this model.

## License

Add your chosen license (for example MIT or Apache-2.0) before public release.
