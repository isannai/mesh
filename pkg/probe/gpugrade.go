package probe

// gpugrade.go: the GPU memory grade a model budget can ask for (MinGrade).
//
// The table is the one in workspace docs/todo/gpu-등급표.md. It is the third copy:
// the rendezvous (servers pkg/rendezvous faucet_qualify.go) and the node's own
// side (glink) each keep one. Change it in all three or the three disagree about
// which node is fit for a budget.
//
// The grade is read from a memory size in GB, summed over the node's GPUs:
//
//	up to 4, 6, 8, 10, 12, 16, 24, 32, 64, 128, 256 GB   grades 1 to 11
//	over 256 GB                                           grade 12
//	under 2 GB, or not read                               no grade (0)
//
// A GPU that is named with memory 0 shares its memory with the CPU (Jetson, DGX
// Spark: nvidia-smi gives no figure), so the system RAM stands for it, once.

import (
	"math"
	"strings"
)

// maxGPUGrade is the highest grade.
const maxGPUGrade = 12

// gradeLimitsGB are the upper bounds (inclusive) of grades 1 to 11 in GB. A larger
// memory is grade 12. A grade is the first bound the memory fits under.
var gradeLimitsGB = [...]float64{4, 6, 8, 10, 12, 16, 24, 32, 64, 128, 256}

// minGradeMemoryGB is the smallest memory that has a grade.
const minGradeMemoryGB = 2

// gpuGrade is the grade (1 to 12) a memory size falls in, 0 when it has none.
func gpuGrade(gb float64) int {
	if gb < minGradeMemoryGB {
		return 0
	}
	for i, limit := range gradeLimitsGB {
		if gb <= limit {
			return i + 1
		}
	}
	return maxGPUGrade
}

// clampMinGrade reads a budget's minimum grade: 0 for no limit (also for a value
// below 0), and never above the highest grade.
func clampMinGrade(n int) int {
	switch {
	case n <= 0:
		return 0
	case n > maxGPUGrade:
		return maxGPUGrade
	}
	return n
}

// memoryGB is the memory the grade is read from, in GB to 0.1. GPUs add up. A GPU
// with a name and no memory figure shares memory with the CPU, so the RAM is added
// once for it.
func (i NodeProbeInfo) memoryGB() float64 {
	var total float64
	unified := false
	for _, g := range i.GPUs {
		switch {
		case g.MemoryGB > 0:
			total += g.MemoryGB
		case strings.TrimSpace(g.Name) != "":
			unified = true
		}
	}
	if unified {
		total += i.RAMGB
	}
	return math.Round(total*10) / 10
}
