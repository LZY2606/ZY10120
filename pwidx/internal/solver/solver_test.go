package solver

import (
	"context"
	"math"
	"testing"

	"pwidx/internal/diffract"
	"pwidx/internal/extinction"
	"pwidx/internal/fixtures"
)

func fixtureConfig(t *testing.T, name string) Config {
	t.Helper()
	var spec *fixtures.Spec
	for i := range fixtures.Catalog() {
		if fixtures.Catalog()[i].Name == name {
			sp := fixtures.Catalog()[i]
			spec = &sp
		}
	}
	if spec == nil {
		t.Fatalf("fixture %s 不存在", name)
	}
	obs := fixtures.Generate(*spec)
	peaks := make([]diffract.Peak, 0, len(obs))
	for i, o := range obs {
		peaks = append(peaks, diffract.Peak{Index: i, Position: o.TwoTheta, Sigma: o.Sigma, Intensity: o.Intensity})
	}
	return Config{
		Peaks: peaks, Unit: diffract.UnitTwoTheta, Wavelength: spec.Wavelength,
		Systems:   append([]diffract.System(nil), diffract.AllSystems...),
		Rules:     append([]extinction.Rule(nil), spec.Rules...),
		MaxVolume: 5000, Seed: 42, TopK: 8, RandomTrials: 120, RefineEvals: 100,
	}
}

func TestCubicRecovery(t *testing.T) {
	cfg := fixtureConfig(t, "cubic_F_NaCl_like")
	cfg.Systems = []diffract.System{diffract.Cubic, diffract.Tetragonal}
	res := Solve(context.Background(), cfg)
	if res.Status != "ok" {
		t.Fatalf("status=%s issues=%v", res.Status, res.Issues)
	}
	if len(res.Candidates) == 0 {
		t.Fatal("无候选")
	}
	top := res.Candidates[0]
	if top.ReducedCell.System != diffract.Cubic {
		t.Fatalf("最高分解候选应为 cubic，got %s (score %.3f)", top.ReducedCell.System, top.Score.Score)
	}
	if math.Abs(top.ReducedCell.A-5.64) > 0.05 {
		t.Fatalf("恢复 a=%.4f，偏离 5.64 超过 0.05 Å", top.ReducedCell.A)
	}
	if top.Score.CoverageTerm < 0.8 {
		t.Fatalf("覆盖率过低: %.3f", top.Score.CoverageTerm)
	}
	// F 消光：匹配到的 hkl 必须通过 F 规则。
	for _, m := range top.Score.Matches {
		for _, h := range m.HKLs {
			ok, rule := extinction.Allowed(h[0], h[1], h[2], cfg.Rules)
			if !ok {
				t.Fatalf("被消光 hkl %v 出现在匹配中（规则 %s）", h, rule)
			}
		}
	}
}

func TestParallelismInvariance(t *testing.T) {
	base := fixtureConfig(t, "tetragonal_P_rutile_like")
	base.Systems = []diffract.System{diffract.Tetragonal}
	run := func(w int) []Candidate {
		cfg := base
		cfg.Workers = w
		res := Solve(context.Background(), cfg)
		if res.Status != "ok" {
			t.Fatalf("workers=%d status=%s", w, res.Status)
		}
		return res.Candidates
	}
	c1 := run(1)
	c8 := run(8)
	if len(c1) != len(c8) {
		t.Fatalf("候选数随并行度变化: %d vs %d", len(c1), len(c8))
	}
	for i := range c1 {
		if c1[i].CanonicalKey != c8[i].CanonicalKey {
			t.Fatalf("第 %d 名候选随并行度变化:\n%+v\nvs\n%+v", i, c1[i].ReducedCell, c8[i].ReducedCell)
		}
		if math.Abs(c1[i].Score.Score-c8[i].Score.Score) > 1e-12 {
			t.Fatalf("第 %d 名分数随并行度变化: %.12f vs %.12f", i, c1[i].Score.Score, c8[i].Score.Score)
		}
	}
}

func TestSeedDeterminism(t *testing.T) {
	base := fixtureConfig(t, "hexagonal_P_graphite_like")
	base.Systems = []diffract.System{diffract.Hexagonal}
	r1 := Solve(context.Background(), base)
	r2 := Solve(context.Background(), base)
	if r1.Fingerprint != r2.Fingerprint {
		t.Fatal("相同输入指纹不同")
	}
	if len(r1.Candidates) != len(r2.Candidates) {
		t.Fatal("相同种子候选数不同")
	}
	for i := range r1.Candidates {
		if r1.Candidates[i].ID != r2.Candidates[i].ID || r1.Candidates[i].CanonicalKey != r2.Candidates[i].CanonicalKey {
			t.Fatal("相同种子结果不确定")
		}
	}
}

func TestVolumeCapFilters(t *testing.T) {
	cfg := fixtureConfig(t, "cubic_F_NaCl_like")
	cfg.Systems = []diffract.System{diffract.Cubic}
	cfg.MaxVolume = 50 // NaCl 胞 ~179 Å³
	res := Solve(context.Background(), cfg)
	for _, c := range res.Candidates {
		if c.Volume > 50*(1+1e-9) {
			t.Fatalf("超体积候选被发布: V=%.1f", c.Volume)
		}
	}
}

func TestAbortPublishesNothing(t *testing.T) {
	cfg := fixtureConfig(t, "monoclinic_P_generic")
	cfg.Systems = []diffract.System{diffract.Monoclinic}
	cfg.RandomTrials = 5000
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 进入前即取消
	res := Solve(ctx, cfg)
	if !res.Aborted || res.Status != "aborted" {
		t.Fatalf("期望 aborted，got status=%s", res.Status)
	}
	if len(res.Candidates) != 0 {
		t.Fatal("中止后不得发布半成品 Top-K")
	}
}

func TestPeakOverridesDoNotMutateInput(t *testing.T) {
	cfg := fixtureConfig(t, "cubic_F_NaCl_like")
	cfg.Systems = []diffract.System{diffract.Cubic}
	explainedAll := Solve(context.Background(), cfg)
	cfg2 := cfg
	cfg2.PeakOverrides = map[int]bool{0: true, 1: true}
	res := Solve(context.Background(), cfg2)
	// 原始峰表仍在
	if cfg.Peaks[0].Position <= 0 {
		t.Fatal("原始峰被改动")
	}
	if len(res.NormPeaks) != len(cfg.Peaks) {
		t.Fatal("归一化峰数量改变")
	}
	exCount := 0
	for _, p := range res.NormPeaks {
		if p.Excluded {
			exCount++
		}
	}
	if exCount != 2 {
		t.Fatalf("应有 2 个排除峰，got %d", exCount)
	}
	if explainedAll.Candidates[0].Score.ObservedUsed <= res.Candidates[0].Score.ObservedUsed {
		// 排除后 used 必须严格下降（只要 Top1 仍是同一晶格）
		if explainedAll.Candidates[0].CanonicalKey == res.Candidates[0].CanonicalKey &&
			explainedAll.Candidates[0].Score.ObservedUsed == res.Candidates[0].Score.ObservedUsed {
			t.Fatal("排除覆盖未生效")
		}
	}
}

func TestAmbiguityListed(t *testing.T) {
	// 高对称人造数据：同一 d 多个等价 hkl（如 cubic (100)/(010)），歧义须被列出。
	cfg := fixtureConfig(t, "cubic_F_NaCl_like")
	res := Solve(context.Background(), cfg)
	top := res.Candidates[0]
	amb := 0
	for _, m := range top.Score.Matches {
		if m.Ambiguous {
			amb++
			if len(m.HKLs) < 2 {
				t.Fatal("歧义标记但只有一个 hkl")
			}
		}
	}
	// NaCl 图样里 (200) 类高对称峰必然产生简并。
	if amb == 0 {
		t.Fatal("cubic 图样应至少有一个简并峰被标注为歧义")
	}
}
