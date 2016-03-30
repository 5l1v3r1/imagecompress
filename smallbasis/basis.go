package smallbasis

import (
	"math"

	"github.com/unixpickle/num-analysis/linalg"
)

// BasisMatrix generates a column matrix for
// the standard image basis elements.
func BasisMatrix(size int) *linalg.Matrix {
	res := linalg.NewMatrix(size, size)
	for i := 0; i < size/2; i++ {
		freq := float64(i+1) * 2 * math.Pi / float64(size)
		for j := 0; j < size; j++ {
			argument := float64(j)
			res.Set(j, 2*i, math.Cos(argument*freq))
			res.Set(j, 2*i+1, math.Sin(argument*freq))
		}
	}

	// The last sin() should be replaced with cos(0*N) in every case,
	// since the basis needs a vector of all 1's.
	for i := 0; i < size; i++ {
		res.Set(i, size-1, 1)
	}

	normalizeColumns(res)

	return res
}

// OrthoBasis generates an NxN basis with recursively
// orthogonal columns.
// The size of the basis must be a power of two.
//
// The matrix is generated by computing A=OrthoBasis(N/2),
// assuming that A has orthogonal columns, and then
// generating a new orthogonal matrix [A -A; A A].
// As a base case, OrthoBasis(1) is the 1x1 identity.
func OrthoBasis(size int) *linalg.Matrix {
	if size == 1 {
		res := linalg.NewMatrix(1, 1)
		res.Set(0, 0, 1)
		return res
	}

	half := size / 2
	if half*2 != size {
		panic("size is not a power of two")
	}
	subMatrix := OrthoBasis(half)

	res := linalg.NewMatrix(size, size)
	for i := 0; i < half; i++ {
		for j := 0; j < half; j++ {
			e := subMatrix.Get(i, j)
			res.Set(i, j, e)
			res.Set(i+half, j, e)
			res.Set(i+half, j+half, e)
			res.Set(i, j+half, -e)
		}
	}

	return res
}

func normalizeColumns(m *linalg.Matrix) {
	vec := make(linalg.Vector, m.Rows)
	for col := 0; col < m.Rows; col++ {
		for row := 0; row < m.Rows; row++ {
			vec[row] = m.Get(row, col)
		}
		invMag := 1.0 / math.Sqrt(vec.Dot(vec))
		for row := 0; row < m.Rows; row++ {
			m.Set(row, col, vec[row]*invMag)
		}
	}
}
