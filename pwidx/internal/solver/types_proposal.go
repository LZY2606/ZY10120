package solver

import "pwidx/internal/diffract"

// proposal 为待打分的候选建议。
type proposal struct {
	cell   diffract.Cell
	source string
}
