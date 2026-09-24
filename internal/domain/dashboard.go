package domain

import "time"

type Filter struct {
	Mode   string
	Period string
	Date   time.Time
	Branch string
}

type Point struct {
	Label string
	Value int64
}

type Series struct {
	Name   string
	Points []Point
}

type Metric struct {
	Label    string
	Value    int64
	Previous int64
	Unit     string // rupiah, percent, or count; percent values are hundredths of a point
	Domain   string
	Category string
	Key      string
	Bucket   string
}

type Dashboard struct {
	Title      string
	Subtitle   string
	Metrics    []Metric
	Secondary  []Metric
	Series     []Series
	Groups     []Group
	Maturities []Maturity
	Empty      bool
}

type Group struct {
	Title   string
	Metrics []Metric
}

type Maturity struct {
	Name, Account, Branch string
	Due                   time.Time
	Amount                int64
}

type NominativeFilter struct {
	Filter
	Domain, Category, Metric, Search string
	Bucket                           string
	Page                             int
}

type Record struct {
	Name, Account, CIF, Branch, Product, Collectibility string
	Amount, Outstanding                                 int64
	Due                                                 time.Time
}

type NominativeResult struct {
	Title, MetricLabel string
	Rows               []Record
	Total, Filtered    int64
	Count, FilterCount int
	Page, Pages        int
}
