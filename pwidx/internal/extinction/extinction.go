// Package extinction 实现常见 Bravais 格子与代表性空间群的系统消光。
// 被规则排除的 hkl 不会进入预测反射表，因此不能作为匹配证据。
package extinction

import "fmt"

// Rule 为一条消光规则。None 表示不应用任何消光。
type Rule string

const (
	None          Rule = "none"
	P             Rule = "P" // 简单格子，无消光
	IBodyCentered Rule = "I" // h+k+l = 2n
	FBaseCentered Rule = "F" // h,k,l 全奇或全偶
	CBaseCentered Rule = "C" // h+k = 2n
	ABaseCentered Rule = "A" // k+l = 2n
	BBaseCentered Rule = "B" // h+l = 2n
	RhombohedralR Rule = "R" // -h+k+l = 3n（六方取向下的菱方格子）
	// 代表性螺旋轴/滑移面消光（与上述格子规则可组合）
	Screw21_00l Rule = "21_00l" // 00l: l=2n
	Screw21_h00 Rule = "21_h00" // h00: h=2n
	Screw21_0k0 Rule = "21_0k0" // 0k0: k=2n
	Screw41_00l Rule = "41_00l" // 00l: l=4n
	Screw31_00l Rule = "31_00l" // 00l: l=3n (3_1/3_2)
	Screw61_00l Rule = "61_00l" // 00l: l=6n (6_1/6_5)
	GlideB_b    Rule = "b_h0l"  // h0l: h=2n (b-glide 垂直 b)
	GlideC_c    Rule = "c_0kl"  // 0kl: l=2n (c-glide 垂直 a)
	GlideN_h0l  Rule = "n_h0l"  // h0l: h+l=2n
	Diamond     Rule = "d_F"    // F 格子且全偶时 h+k+l=4n（如 Fd-3m）
)

// Catalog 给出可供界面选择的规则及说明。
var Catalog = []struct {
	Rule Rule
	Name string
	Desc string
}{
	{None, "无消光", "不应用系统消光（P 格子且忽略螺旋/滑移）"},
	{P, "P 简单格子", "无格子消光"},
	{IBodyCentered, "I 体心", "h+k+l 为奇数时消光"},
	{FBaseCentered, "F 面心", "h,k,l 必须全奇或全偶"},
	{CBaseCentered, "C 底心", "h+k 为奇数时消光"},
	{ABaseCentered, "A 底心", "k+l 为奇数时消光"},
	{BBaseCentered, "B 底心", "h+l 为奇数时消光"},
	{RhombohedralR, "R 菱心(六方取向)", "-h+k+l 不为 3 的倍数时消光"},
	{Screw21_00l, "2₁ 螺旋轴 //c", "00l 中 l 为奇数时消光"},
	{Screw21_h00, "2₁ 螺旋轴 //a", "h00 中 h 为奇数时消光"},
	{Screw21_0k0, "2₁ 螺旋轴 //b", "0k0 中 k 为奇数时消光"},
	{Screw41_00l, "4₁/4₃ 螺旋轴 //c", "00l 中 l 不是 4 的倍数时消光"},
	{Screw31_00l, "3₁/3₂ 螺旋轴 //c", "00l 中 l 不是 3 的倍数时消光"},
	{Screw61_00l, "6₁/6₅ 螺旋轴 //c", "00l 中 l 不是 6 的倍数时消光"},
	{GlideB_b, "b 滑移面 ⊥b", "h0l 中 h 为奇数时消光"},
	{GlideC_c, "c 滑移面 ⊥a", "0kl 中 l 为奇数时消光"},
	{GlideN_h0l, "n 滑移面 ⊥b", "h0l 中 h+l 为奇数时消光"},
	{Diamond, "金刚石 Fd 型", "全偶反射还需 h+k+l=4n"},
}

// Valid 校验规则名。
func Valid(r Rule) bool {
	for _, e := range Catalog {
		if e.Rule == r {
			return true
		}
	}
	return false
}

// Allowed 报告反射 (h,k,l) 是否能通过全部给定规则。
func Allowed(h, k, l int, rules []Rule) (bool, Rule) {
	for _, r := range rules {
		switch r {
		case None, P:
			// 无消光
		case IBodyCentered:
			if (h+k+l)%2 != 0 {
				return false, r
			}
		case FBaseCentered:
			allOdd := h%2 != 0 && k%2 != 0 && l%2 != 0
			allEven := h%2 == 0 && k%2 == 0 && l%2 == 0
			if !allOdd && !allEven {
				return false, r
			}
		case CBaseCentered:
			if (h+k)%2 != 0 {
				return false, r
			}
		case ABaseCentered:
			if (k+l)%2 != 0 {
				return false, r
			}
		case BBaseCentered:
			if (h+l)%2 != 0 {
				return false, r
			}
		case RhombohedralR:
			if (-h+k+l)%3 != 0 {
				return false, r
			}
		case Screw21_00l:
			if h == 0 && k == 0 && l%2 != 0 {
				return false, r
			}
		case Screw21_h00:
			if k == 0 && l == 0 && h%2 != 0 {
				return false, r
			}
		case Screw21_0k0:
			if h == 0 && l == 0 && k%2 != 0 {
				return false, r
			}
		case Screw41_00l:
			if h == 0 && k == 0 && l%4 != 0 {
				return false, r
			}
		case Screw31_00l:
			if h == 0 && k == 0 && l%3 != 0 {
				return false, r
			}
		case Screw61_00l:
			if h == 0 && k == 0 && l%6 != 0 {
				return false, r
			}
		case GlideB_b:
			if k == 0 && h%2 != 0 {
				return false, r
			}
		case GlideC_c:
			if h == 0 && l%2 != 0 {
				return false, r
			}
		case GlideN_h0l:
			if k == 0 && (h+l)%2 != 0 {
				return false, r
			}
		case Diamond:
			allEven := h%2 == 0 && k%2 == 0 && l%2 == 0
			if allEven && (h+k+l)%4 != 0 {
				return false, r
			}
		default:
			return false, r
		}
	}
	return true, ""
}

// ValidateRules 返回未知规则名错误。
func ValidateRules(rules []Rule) error {
	for _, r := range rules {
		if !Valid(r) {
			return fmt.Errorf("未知消光规则 %q", r)
		}
	}
	return nil
}
