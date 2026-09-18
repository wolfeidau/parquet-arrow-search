package query

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

// This deliberately small grammar accepts one predicate, not full SQL.
// SQL strings use doubled quotes; backslashes are preserved for regex patterns.
var sqlParser = participle.MustBuild[statement](
	participle.Lexer(lexer.MustSimple([]lexer.SimpleRule{
		{Name: "Whitespace", Pattern: `\s+`},
		{Name: "String", Pattern: `'(?:[^']|'')*'`},
		{Name: "QuotedIdent", Pattern: `"(?:[^"]|"")*"`},
		{Name: "Keyword", Pattern: `(?i:SELECT|FROM|WHERE|LIMIT|IS|NOT|NULL|REGEX)\b`},
		{Name: "Ident", Pattern: `[a-zA-Z_][a-zA-Z0-9_]*`},
		{Name: "Int", Pattern: `[+-]?[0-9]+`},
		{Name: "Operator", Pattern: `!=|<>|<=|>=|[=<>]`},
		{Name: "Punct", Pattern: `[*,();]`},
	})),
	participle.Elide("Whitespace"),
	participle.CaseInsensitive("Keyword"),
)

type statement struct {
	Select    selection     `parser:"'SELECT' @@"`
	Table     identifier    `parser:"'FROM' @@"`
	Where     *sqlPredicate `parser:"('WHERE' @@)?"`
	Limit     *string       `parser:"('LIMIT' @Int)?"`
	Semicolon bool          `parser:"@';'?"`
}

type selection struct {
	All     bool         `parser:" @'*'"`
	Columns []identifier `parser:"| @@ (',' @@)*"`
}

type identifier struct {
	Text string `parser:"@(Ident | QuotedIdent)"`
}

func (i identifier) name() string {
	if strings.HasPrefix(i.Text, `"`) {
		return strings.ReplaceAll(i.Text[1:len(i.Text)-1], `""`, `"`)
	}
	return i.Text
}

type sqlPredicate struct {
	Regex  *regexPredicate  `parser:" @@"`
	Column *columnPredicate `parser:"| @@"`
}

type regexPredicate struct {
	Column  identifier `parser:"'REGEX' '(' @@"`
	Pattern string     `parser:"',' @String ')'"`
}

type columnPredicate struct {
	Column     identifier           `parser:"@@"`
	Null       *nullPredicate       `parser:"( @@"`
	Comparison *comparisonPredicate `parser:"| @@ )"`
}

type nullPredicate struct {
	Not bool `parser:"'IS' @'NOT'? 'NULL'"`
}

type comparisonPredicate struct {
	Op    string     `parser:"@Operator"`
	Value sqlLiteral `parser:"@@"`
}

type sqlLiteral struct {
	String  *string `parser:" @String"`
	Integer *string `parser:"| @Int"`
}

func unquoteSQL(s string) string {
	return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
}

func parseQuery(text string) (*statement, error) {
	q, err := sqlParser.ParseString("query", text)
	if err != nil {
		return nil, fmt.Errorf("parse query (expected SELECT columns FROM logs [WHERE predicate] [LIMIT n]): %w", err)
	}

	// Table identifiers follow the same case-sensitive rule as column names.
	if q.Table.name() != "logs" {
		return nil, fmt.Errorf("unknown table %q; use logs for the file supplied by --file", q.Table.name())
	}
	return q, nil
}

func (q *statement) rowLimit() (int64, error) {
	if q.Limit == nil {
		return -1, nil
	}

	n, err := strconv.ParseInt(*q.Limit, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse LIMIT: %w", err)
	}
	if n < 0 {
		return 0, fmt.Errorf("LIMIT must be non-negative")
	}
	return n, nil
}
