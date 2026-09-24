package view

import (
	"fmt"
	"html/template"
	"io"
	"math"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/ibldzn/dashboard-roro-jongrang/internal/domain"
)

type Renderer struct{ templates map[string]*template.Template }

func New(dir string) (*Renderer, error) {
	r := &Renderer{templates: make(map[string]*template.Template)}
	for _, page := range []string{"login", "dashboard", "nominative"} {
		t, err := template.New("layout").Funcs(FuncMap()).ParseFiles(filepath.Join(dir, "layout.html"), filepath.Join(dir, "partials.html"), filepath.Join(dir, page+".html"))
		if err != nil {
			return nil, err
		}
		r.templates[page] = t
	}
	return r, nil
}

func (r *Renderer) Render(w io.Writer, page string, data any, partial bool) error {
	t := r.templates[page]
	if t == nil {
		return fmt.Errorf("unknown template %q", page)
	}
	if partial {
		return t.ExecuteTemplate(w, "content", data)
	}
	return t.ExecuteTemplate(w, "layout", data)
}

func Integer(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "." + s[i:]
	}
	return s
}

func Rupiah(n int64) string {
	neg := ""
	if n < 0 {
		neg = "−"
		n = -n
	}
	if n < 1000 {
		return neg + "Rp " + Integer(n)
	}
	units := []struct {
		value  int64
		suffix string
	}{{1_000_000_000_000, "T"}, {1_000_000_000, "M"}, {1_000_000, "Jt"}, {1_000, "Rb"}}
	for _, u := range units {
		if n >= u.value {
			v := float64(n) / float64(u.value)
			dec := 2
			if v >= 10 {
				dec = 1
			}
			return neg + "Rp " + strings.Replace(strconv.FormatFloat(math.Round(v*math.Pow10(dec))/math.Pow10(dec), 'f', dec, 64), ".", ",", 1) + " " + u.suffix
		}
	}
	return neg + "Rp " + Integer(n)
}

func Percent(hundredths int64) string {
	return strings.Replace(strconv.FormatFloat(float64(hundredths)/100, 'f', 2, 64), ".", ",", 1) + "%"
}

func DateID(t time.Time) string {
	months := []string{"", "Januari", "Februari", "Maret", "April", "Mei", "Juni", "Juli", "Agustus", "September", "Oktober", "November", "Desember"}
	return fmt.Sprintf("%02d %s %d", t.Day(), months[t.Month()], t.Year())
}

func FuncMap() template.FuncMap {
	return template.FuncMap{
		"money": Rupiah, "integer": func(n any) string {
			switch v := n.(type) {
			case int:
				return Integer(int64(v))
			case int64:
				return Integer(v)
			default:
				return "0"
			}
		}, "percent": Percent,
		"date":     DateID,
		"clockWIB": func(t time.Time) string { return t.In(time.FixedZone("WIB", 7*3600)).Format("15:04") },
		"display": func(unit string, value int64) string {
			switch unit {
			case "percent":
				return Percent(value)
			case "count":
				return Integer(value) + " rekening"
			default:
				return Rupiah(value)
			}
		},
		"delta": func(value, previous int64) string {
			if previous == 0 {
				return "—"
			}
			if value == previous {
				return "→ 0,0%"
			}
			d := float64(value-previous) * 100 / float64(previous)
			return strings.Replace(fmt.Sprintf("%+.1f%%", d), ".", ",", 1)
		},
		"add": func(a, b int) int { return a + b }, "sub": func(a, b int) int { return a - b },
		"metricView": func(f domain.Filter, m domain.Metric) any {
			return struct {
				domain.Metric
				Filter domain.Filter
			}{m, f}
		},
		"seq": func(from, to int) []int {
			var out []int
			for i := from; i <= to; i++ {
				out = append(out, i)
			}
			return out
		},
		"branch":     func(n int) string { return fmt.Sprintf("%03d", n) },
		"categories": func() []string { return []string{"abp", "dpk", "jatuh-tempo"} },
		"upper":      strings.ToUpper,
		"link":       func(path string, f domain.Filter) string { return link(path, f, nil) },
		"tablink": func(path string, f domain.Filter, category string) string {
			return link(path, f, map[string]string{"category": category})
		},
		"detail": func(f domain.Filter, m domain.Metric) string {
			return link("/nominatif", f, map[string]string{"domain": m.Domain, "category": m.Category, "metric": m.Key, "bucket": m.Bucket})
		},
		"pageLink": func(f domain.Filter, domain, category, metric, bucket, search string, page int) string {
			return link("/nominatif", f, map[string]string{"domain": domain, "category": category, "metric": metric, "bucket": bucket, "search": search, "page": strconv.Itoa(page)})
		},
	}
}

func link(path string, f domain.Filter, extra map[string]string) string {
	q := url.Values{"mode": {f.Mode}, "period": {f.Period}, "branch": {f.Branch}}
	for k, v := range extra {
		if v != "" {
			q.Set(k, v)
		}
	}
	return path + "?" + q.Encode()
}
