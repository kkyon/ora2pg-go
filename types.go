package main

import "database/sql"

// Config represents the export configuration
type Config struct {
	OracleDSN       string
	OracleUser      string
	OraclePwd       string
	Schema          string
	PackageAsSchema bool // true: create schema per package, false: use package_name prefix
}

// Column metadata
type Column struct {
	Name            string
	DataType        string
	Nullable        string
	VirtualColumn   string
	DataLength      sql.NullInt64
	DataPrecision   sql.NullInt64
	DataScale       sql.NullInt64
	CharLength      sql.NullInt64
	DataDefault     sql.NullString
	ColumnID        int
}

type ColumnComment struct {
	ColumnName string
	Comment    string
}

type Constraint struct {
	Name           string
	Type           string
	Condition      string
	Columns        []string
	IndexName      string
}

type Index struct {
	Name             string
	Uniqueness       string
	Columns          []string
	ConstraintBacked bool
}

type ForeignKey struct {
	Name              string
	Columns           []string
	ReferencedTable   string
	ReferencedColumns []string
}

// Table metadata
type Table struct {
	Name              string
	Columns           []*Column
	PrimaryKey        *Constraint
	UniqueConstraints []*Constraint
	CheckConstraints  []*Constraint
	Indexes           []*Index
	ForeignKeys       []*ForeignKey
	ColumnComments    []*ColumnComment
	Comment           string
	PartitioningType  string
	PartitionKey      []string
}

// Type/Object Type metadata
type TypeField struct {
	Name     string
	DataType string
}

type TypeDef struct {
	Name   string
	Owner  string
	Fields []*TypeField
}

// Sequence metadata
type Sequence struct {
	Name        string
	Increment   int64
	MinValue    int64
	MaxValue    int64
	LastNumber  int64
	CacheSize   int64
	CycleFlag   string
}

// View metadata
type View struct {
	Name       string
	Text       string
	Comment    string
	ColumnList []string
}

// Materialized View metadata
type Mview struct {
	Name    string
	Query   string
	Comment string
}

// Procedure metadata
type Procedure struct {
	Name   string
	Owner  string
	Params []*ProcParam
	Body   string
}

type ProcParam struct {
	Name     string
	DataType string
	InOut    string
	Position int
}

// Function metadata
type Function struct {
	Name       string
	Owner      string
	Params     []*FuncParam
	ReturnType string
	RawSource  string
}

type FuncParam struct {
	Name         string
	DataType     string
	InOut        string
	Position     int
	Defaulted    bool
	DefaultValue string
}

// Package metadata
type Package struct {
	Name       string
	Owner      string
	Domains    []*Domain
	Types      []*PackageType
	Functions  []*Function
	Procedures []*Procedure
}

type Domain struct {
	Name     string
	BaseType string
}

type PackageType struct {
	Name       string
	TypeKind   string
	Definition string
}
