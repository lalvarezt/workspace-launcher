package main

type candidateColumn int

const (
	candidateColumnRoot candidateColumn = iota
	candidateColumnName
	candidateColumnGit
	candidateColumnLanguage
	candidateColumnAge
)

func visibleCandidateColumns(cfg config) []candidateColumn {
	columns := make([]candidateColumn, 0, 5)
	if cfg.showRoot {
		columns = append(columns, candidateColumnRoot)
	}
	columns = append(columns, candidateColumnName)
	if cfg.showGit {
		columns = append(columns, candidateColumnGit)
	}
	if cfg.showLanguage {
		columns = append(columns, candidateColumnLanguage)
	}
	columns = append(columns, candidateColumnAge)
	return columns
}

func candidateColumnSearchable(column candidateColumn) bool {
	switch column {
	case candidateColumnRoot, candidateColumnName, candidateColumnGit:
		return true
	default:
		return false
	}
}
