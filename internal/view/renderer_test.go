package view

import "testing"

func TestIndonesianFormatting(t *testing.T) {
	if got := Rupiah(128_400_000_000); got != "Rp 128,4 M" {
		t.Fatalf("money: %s", got)
	}
	if got := Rupiah(8_210_000_000); got != "Rp 8,21 M" {
		t.Fatalf("money: %s", got)
	}
	if got := Percent(8142); got != "81,42%" {
		t.Fatalf("percent: %s", got)
	}
	if got := Integer(1248); got != "1.248" {
		t.Fatalf("integer: %s", got)
	}
}
