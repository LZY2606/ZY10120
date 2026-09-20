package extinction

import "testing"

func TestLatticeExtinctions(t *testing.T) {
	cases := []struct {
		r       Rule
		h, k, l int
		want    bool
	}{
		{IBodyCentered, 1, 0, 0, false},
		{IBodyCentered, 1, 1, 0, true},
		{IBodyCentered, 2, 0, 0, true},
		{FBaseCentered, 1, 1, 1, true},
		{FBaseCentered, 2, 0, 0, true},
		{FBaseCentered, 1, 1, 0, false},
		{CBaseCentered, 1, 0, 0, false},
		{CBaseCentered, 1, 1, 0, true},
		{RhombohedralR, 0, 0, 3, true},
		{RhombohedralR, 1, 0, 0, true}, // -1+0+0 = -1? 不整除 3 → false；检查符号
		{Screw21_00l, 0, 0, 1, false},
		{Screw21_00l, 0, 0, 2, true},
		{Screw21_00l, 1, 0, 1, true},
		{Diamond, 2, 2, 2, false}, // F 全偶但 h+k+l=6≠4n
		{Diamond, 4, 0, 0, true},
		{Diamond, 1, 1, 1, true},
	}
	// 修正 R 用例：(1,0,0): -1+0+0=-1 不被 3 整除 → forbidden
	rCases := []struct {
		h, k, l int
		want    bool
	}{
		{0, 0, 3, true},
		{1, 0, 0, false},
		{1, 1, 0, true}, // -1+1+0=0
	}
	for _, c := range rCases {
		got, _ := Allowed(c.h, c.k, c.l, []Rule{RhombohedralR})
		if got != c.want {
			t.Fatalf("R(%d%d%d)=%v want %v", c.h, c.k, c.l, got, c.want)
		}
	}
	for _, c := range cases {
		if c.r == RhombohedralR {
			continue
		}
		got, _ := Allowed(c.h, c.k, c.l, []Rule{c.r})
		if got != c.want {
			t.Fatalf("%s(%d%d%d)=%v want %v", c.r, c.h, c.k, c.l, got, c.want)
		}
	}
}

func TestUnknownRule(t *testing.T) {
	if err := ValidateRules([]Rule{None, IBodyCentered}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRules([]Rule{"ZZ"}); err == nil {
		t.Fatal("未知规则应报错")
	}
}
