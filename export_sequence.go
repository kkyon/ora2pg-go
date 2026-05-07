package main

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"
)

// SEQUENCE export with metadata-driven approach

func loadSequences(db *sql.DB) ([]*Sequence, error) {
	rows, err := db.Query(`
SELECT sequence_name,
       increment_by,
       min_value,
       max_value,
       last_number,
       cache_size,
       cycle_flag
FROM user_sequences
WHERE sequence_name NOT LIKE 'ISEQ$$_%'
ORDER BY sequence_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	seqs := make([]*Sequence, 0)
	for rows.Next() {
		var name string
		var increment, minVal, lastNum, cacheSize int64
		var maxValStr string
		var cycleFlag string
		if err := rows.Scan(&name, &increment, &minVal, &maxValStr, &lastNum, &cacheSize, &cycleFlag); err != nil {
			return nil, err
		}

		// Parse MAX_VALUE from string, if it's too large set to 0 (signals NO MAXVALUE)
		maxVal := int64(0)
		if maxValStr != "" {
			if parsed, err := strconv.ParseInt(maxValStr, 10, 64); err == nil {
				maxVal = parsed
			}
		}

		seqs = append(seqs, &Sequence{
			Name:       name,
			Increment:  increment,
			MinValue:   minVal,
			MaxValue:   maxVal,
			LastNumber: lastNum,
			CacheSize:  cacheSize,
			CycleFlag:  cycleFlag,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sort.Slice(seqs, func(i, j int) bool { return seqs[i].Name < seqs[j].Name })
	return seqs, nil
}

func renderSequences(seqs []*Sequence) string {
	var b strings.Builder

	for _, seq := range seqs {
		b.WriteString("CREATE SEQUENCE ")
		b.WriteString(strings.ToLower(seq.Name))
		b.WriteString(" INCREMENT ")
		b.WriteString(strconv.FormatInt(seq.Increment, 10))

		if seq.MinValue <= -9223372036854775808/2 {
			b.WriteString(" MINVALUE 1")
		} else {
			b.WriteString(" MINVALUE ")
			b.WriteString(strconv.FormatInt(seq.MinValue, 10))
		}

		b.WriteString(" NO MAXVALUE")
		b.WriteString(" START ")
		b.WriteString(strconv.FormatInt(seq.LastNumber, 10))
		b.WriteString(";\n")
	}

	return b.String()
}
