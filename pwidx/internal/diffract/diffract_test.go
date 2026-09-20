package diffract

import (
	"math"
	"testing"
)

func approxEq(t *testing.T, got, want, tol float64, name string) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %.6f, want %.6f", name, got, want)
	}
}

func TestBraggRoundTrip(t *testing.T) {
	const lambda = 1.5406
	for _, d := range []float64{1.0, 2.0, 3.14} {
		tt, err := TwoTheta(d, lambda)
		if err != nil {
			t.Fatalf("TwoTheta(%v): %v", d, err)
		}
		d2, err := DFromTwoTheta(tt, lambda)
		if err != nil {
			t.Fatalf("DFromTwoTheta: %v", err)
		}
		approxEq(t, d2, d, 1e-9, "d roundtrip")
	}
}

func TestArcsinDomainError(t *testing.T) {
	// d < lambda/2 → 2theta 无实数解。
	if _, err := TwoTheta(0.5, 1.5406); err == nil {
		t.Fatal("期望 arcsin 定义域错误")
	}
	if _, err := DFromTwoTheta(0, 1.5406); err == nil {
		t.Fatal("2θ=0 必须报错")
	}
	if _, err := DFromTwoTheta(180, 1.5406); err == nil {
		t.Fatal("2θ=180 必须报错")
	}
}

func TestCubicDSpacing(t *testing.T) {
	// a=5.64 的 (111)：d = a/√3
	c := Cell{System: Cubic, A: 5.64, B: 5.64, C: 5.64, Alpha: 90, Beta: 90, Gamma: 90}
	d := c.D(1, 1, 1)
	approxEq(t, d, 5.64/math.Sqrt(3), 1e-9, "d111")
	d002 := c.D(0, 0, 2)
	approxEq(t, d002, 5.64/2, 1e-9, "d002")
	approxEq(t, c.Volume(), 5.64*5.64*5.64, 1e-6, "V")
	if d := c.D(0, 0, 0); !math.IsInf(d, 1) {
		t.Fatalf("(000) d 应为 +Inf, got %v", d)
	}
}

func TestHexagonalMetric(t *testing.T) {
	// 石墨 a=2.464, c=6.712: 1/d(100)^2 = 4/(3a^2)
	c := Cell{System: Hexagonal, A: 2.464, B: 2.464, C: 6.712, Alpha: 90, Beta: 90, Gamma: 120}
	d := c.D(1, 0, 0)
	want := math.Sqrt(3.0) * 2.464 / 2
	approxEq(t, d, want, 1e-9, "hex d100")
}

func TestNormalizeStatuses(t *testing.T) {
	_, issues, status := Normalize(nil, UnitTwoTheta, 1.5406, 0.05, 0.002)
	if status != "error" {
		t.Fatalf("空峰表 status=%s", status)
	}
	if issues[0].Code != "empty_peaks" {
		t.Fatalf("code=%s", issues[0].Code)
	}

	peaks := []Peak{
		{Index: 0, Position: 28.5, Sigma: 0.03, Intensity: 100},
		{Index: 1, Position: 0, Sigma: 0.03},    // 零峰/越界
		{Index: 2, Position: 28.5, Sigma: 0.03}, // 重复
		{Index: 3, Position: 47.3, Sigma: 0.03},
	}
	norm, issues2, status2 := Normalize(peaks, UnitTwoTheta, 1.5406, 0.05, 0.002)
	if status2 != "error" || len(norm) != 3 {
		t.Fatalf("status=%s n=%d issues=%v", status2, len(norm), issues2)
	}
	codes := map[string]bool{}
	for _, is := range issues2 {
		codes[is.Code] = true
	}
	if !codes["out_of_range"] || !codes["duplicate_peak"] {
		t.Fatalf("缺少期望 issue: %v", issues2)
	}

	// d 输入且 d < lambda/2：arcsin 警告但仍可在 d 空间索引。
	dp := []Peak{{Index: 0, Position: 0.7, Sigma: 0.001}, {Index: 1, Position: 2.0, Sigma: 0.002}}
	_, issues3, status3 := Normalize(dp, UnitDSpacing, 1.5406, 0.05, 0.002)
	if status3 != "ok" {
		t.Fatalf("d 空间不应整体失败: %s %v", status3, issues3)
	}
	found := false
	for _, is := range issues3 {
		if is.Code == "arcsin_domain" {
			found = true
		}
	}
	if !found {
		t.Fatal("d<lambda/2 应给 arcsin_domain 状态说明")
	}
}
