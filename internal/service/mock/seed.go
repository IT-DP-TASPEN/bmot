package mock

import (
	"fmt"
	"time"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
)

type account struct {
	name, number, cif, branch, product, category, kind, collectibility string
	base, ceiling, dailyBooking                                        int64
	dueOffset                                                          int
}

var names = []string{"Andi Pratama", "Dewi Lestari", "Budi Santoso", "Rina Maharani", "Agus Setiawan", "Sari Wulandari", "Hendra Wijaya", "Nur Aisyah", "Fajar Ramadhan", "Putri Amelia", "Dimas Saputra", "Intan Permata"}

func seed() []account {
	var out []account
	for b := 0; b <= 8; b++ {
		branch := fmt.Sprintf("%03d", b)
		for specIdx, spec := range []struct {
			kind, category, product string
			count                   int
			base                    int64
		}{
			{"tabungan", "dpk", "Tabungan Umum", 24, 370_000_000},
			{"tabungan", "abp", "Tabungan ABP", 12, 310_000_000},
			{"deposito", "dpk", "Deposito Berjangka", 18, 740_000_000},
			{"deposito", "abp", "Deposito ABP", 10, 620_000_000},
			{"kredit", "organik", "Kredit Modal Kerja", 20, 690_000_000},
			{"kredit", "channeling", "Kredit Channeling", 14, 830_000_000},
		} {
			for i := 0; i < spec.count; i++ {
				base := spec.base + int64((i*7+b*13)%17)*19_000_000
				a := account{
					name: names[(i+b*3)%len(names)], number: fmt.Sprintf("%03d-%02d-%05d", b, specIdx+1, i+1),
					cif: fmt.Sprintf("C%03d%05d", b, i+1), branch: branch, product: spec.product, category: spec.category, kind: spec.kind,
					base: base, ceiling: base * 13 / 10, dailyBooking: base / 850,
					dueOffset: (i*17+b*11)%90 + 1,
				}
				if spec.kind == "kredit" {
					a.collectibility = "Lancar"
					if (i+b*5)%19 == 0 {
						a.collectibility = "Dalam Perhatian Khusus"
					}
					if (i+b*7)%31 == 0 {
						a.collectibility = "Kurang Lancar"
					}
				}
				out = append(out, a)
			}
		}
	}
	return out
}

func balance(a account, date time.Time) int64 {
	if date.Before(domain.FirstMockDate) {
		return 0
	}
	months := (date.Year()-2024)*12 + int(date.Month()) - 9
	// A stable, modest growth pattern; all balance KPIs come from these account snapshots.
	return a.base * int64(1000+months*7+(date.Day()-1)/3) / 1000
}

func dueDate(a account, date time.Time) time.Time {
	due := domain.FirstMockDate.AddDate(0, 0, a.dueOffset)
	for due.Before(date) {
		due = due.AddDate(0, 0, 90)
	}
	return due
}

func booking(a account, start, end time.Time) int64 {
	if end.Before(start) || end.Before(domain.FirstMockDate) {
		return 0
	}
	if start.Before(domain.FirstMockDate) {
		start = domain.FirstMockDate
	}
	var total int64
	for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
		months := (day.Year()-2024)*12 + int(day.Month()) - 9
		season := int64((int(day.Month()) * 7) % 5)
		total += a.dailyBooking * (1000 + int64(months)*7 + season*6) / 1000
	}
	return total
}
