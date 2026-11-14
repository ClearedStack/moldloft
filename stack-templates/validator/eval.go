package validator

import (
	"strings"
	"unicode/utf8"
)

// Fault is one validation finding on a specific line and column.
type Fault struct {
	Line   int    `json:"line"`
	Column string `json:"column"`
	Code   string `json:"code"`
}

// Result is the deterministic outcome of validating input text.
type Result struct {
	Valid      bool    `json:"valid"`
	RowCount   int     `json:"row_count"`
	ErrorCount int     `json:"error_count"`
	Faults     []Fault `json:"faults"`
}

// Evaluate checks input against tool's delimiter, header and column rules.
func Evaluate(tool *Tool, input string) Result {
	tableSets := map[string]map[string]bool{}
	for _, tb := range tool.Tables {
		set := map[string]bool{}
		for _, code := range tb.Codes {
			set[code] = true
		}
		tableSets[tb.ID] = set
	}

	rawLines := strings.Split(strings.ReplaceAll(input, "\r\n", "\n"), "\n")
	type numberedLine struct {
		num  int
		text string
	}
	var nonEmpty []numberedLine
	for i, line := range rawLines {
		trimmed := strings.TrimRight(line, "\r")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		nonEmpty = append(nonEmpty, numberedLine{num: i + 1, text: trimmed})
	}

	faults := []Fault{}
	if len(nonEmpty) == 0 {
		faults = append(faults, Fault{Line: 0, Column: "input", Code: "empty"})
		return Result{Valid: false, RowCount: 0, ErrorCount: 1, Faults: faults}
	}

	startIdx := 0
	if tool.HasHeader {
		hdr := strings.Split(nonEmpty[0].text, tool.Separator)
		startIdx = 1
		if len(hdr) != len(tool.Columns) {
			faults = append(faults, Fault{Line: nonEmpty[0].num, Column: "header", Code: "bad_header"})
		} else {
			for i, col := range tool.Columns {
				if strings.TrimSpace(hdr[i]) != col.ID {
					faults = append(faults, Fault{Line: nonEmpty[0].num, Column: col.ID, Code: "bad_header"})
				}
			}
		}
		if len(nonEmpty) == 1 {
			faults = append(faults, Fault{Line: nonEmpty[0].num, Column: "input", Code: "empty"})
			return Result{Valid: false, RowCount: 0, ErrorCount: len(faults), Faults: faults}
		}
	}

	rowCount := 0
	for _, row := range nonEmpty[startIdx:] {
		rowCount++
		cells := strings.Split(row.text, tool.Separator)
		if len(cells) != len(tool.Columns) {
			faults = append(faults, Fault{Line: row.num, Column: "row", Code: "column_count"})
			continue
		}
		for i, col := range tool.Columns {
			val := strings.TrimSpace(cells[i])
			if val == "" {
				if col.Required {
					faults = append(faults, Fault{Line: row.num, Column: col.ID, Code: "missing"})
				}
				continue
			}
			if !checkCell(col, val, tableSets) {
				faults = append(faults, Fault{Line: row.num, Column: col.ID, Code: "invalid"})
			}
		}
	}
	return Result{
		Valid:      len(faults) == 0,
		RowCount:   rowCount,
		ErrorCount: len(faults),
		Faults:     faults,
	}
}

func checkCell(col Column, val string, tableSets map[string]map[string]bool) bool {
	n := utf8.RuneCountInString(val)
	switch col.Rule {
	case "text":
		return n >= col.MinLen && n <= col.MaxLen
	case "digits":
		if n < col.MinLen || n > col.MaxLen {
			return false
		}
		for i := 0; i < len(val); i++ {
			if val[i] < '0' || val[i] > '9' {
				return false
			}
		}
		return true
	case "upper_code":
		if n < col.MinLen || n > col.MaxLen {
			return false
		}
		if col.Prefix != "" && !strings.HasPrefix(val, col.Prefix) {
			return false
		}
		for i := 0; i < len(val); i++ {
			c := val[i]
			if !((c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-') {
				return false
			}
		}
		return true
	case "in_table":
		return tableSets[col.Table][val]
	default:
		return false
	}
}
