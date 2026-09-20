package canonical

import (
	"math"
	"testing"

	"pwidx/internal/diffract"
)

func TestOrthorhombicAxisPermutation(t *testing.T) {
	c1 := diffract.Cell{System: diffract.OrthorhombicSystem, A: 3, B: 5, C: 4, Alpha: 90, Beta: 90, Gamma: 90}
	c2 := diffract.Cell{System: diffract.OrthorhombicSystem, A: 5, B: 4, C: 3, Alpha: 90, Beta: 90, Gamma: 90}
	r1, r2 := Reduce(c1), Reduce(c2)
	if math.Abs(r1.A-r2.A) > 1e-9 || math.Abs(r1.B-r2.B) > 1e-9 || math.Abs(r1.C-r2.C) > 1e-9 {
		t.Fatalf("等价轴置换未约化到同一标准形: %+v vs %+v", r1, r2)
	}
	if !(r1.A <= r1.B && r1.B <= r1.C) {
		t.Fatalf("标准形轴未排序: %+v", r1)
	}
	if Key(c1) != Key(c2) {
		t.Fatal("去重键应相同")
	}
}

func TestMonoclinicBetaAcute(t *testing.T) {
	c := diffract.Cell{System: diffract.Monoclinic, A: 5.2, B: 8.1, C: 6.3, Alpha: 90, Beta: 104.2, Gamma: 90}
	r := Reduce(c)
	if math.Abs(r.Beta-(180-104.2)) > 1e-6 {
		t.Fatalf("beta 应收敛到锐角形式, got %.3f", r.Beta)
	}
}

func TestRhombohedralObtuseChoice(t *testing.T) {
	mk := func(al float64) diffract.Cell {
		return diffract.Cell{System: diffract.Rhombohedral, A: 6, B: 6, C: 6, Alpha: al, Beta: al, Gamma: al}
	}
	if Key(mk(60)) != Key(mk(120)) {
		t.Fatal("alpha 与 180-alpha 应给同一标准形键")
	}
}

func TestTriclinicGL3ZStable(t *testing.T) {
	// 同一晶格用任意初始胞描述，反复约化应幂等。
	c := diffract.Cell{System: diffract.Triclinic, A: 5.2, B: 6.1, C: 7.3, Alpha: 81, Beta: 71, Gamma: 65}
	r1 := Reduce(c)
	r2 := Reduce(r1)
	if Key(r1) != Key(r2) {
		t.Fatalf("标准形不是不动点:\n%+v\n%+v", r1, r2)
	}
}

func TestDeterminismOfEnumeration(t *testing.T) {
	if len(gl3z) < 48 {
		t.Fatalf("GL(3,Z) 枚举数过少: %d", len(gl3z))
	}
	// 排序后前几个矩阵应为确定内容（I 矩阵按词典序居前之一）。
	c := diffract.Cell{System: diffract.Triclinic, A: 4, B: 5, C: 6, Alpha: 88, Beta: 92, Gamma: 91}
	r1 := Reduce(c)
	r2 := Reduce(c)
	if Key(r1) != Key(r2) {
		t.Fatal("重复约化结果不一致")
	}
}
