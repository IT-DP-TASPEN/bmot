package domain

type BranchOption struct{ Code, Label string }

var Branches = []BranchOption{
	{"ALL", "Konsolidasi"},
	{"001", "001 - KPO"},
	{"002", "002 - KC Bogor"},
	{"003", "003 - KC Depok"},
	{"004", "004 - KC Tangerang"},
	{"005", "005 - KC Jaktim"},
	{"006", "006 - KC Karawang"},
	{"007", "007 - KC Cikarang"},
	{"008", "008 - KC Purwokerto"},
}

func BranchLabel(code string) (string, bool) {
	for _, branch := range Branches {
		if branch.Code == code {
			return branch.Label, true
		}
	}
	return "", false
}
