package smallbasis

import (
	"errors"
	"image"
	"image/color"
	"math"
	"sort"

	"github.com/unixpickle/num-analysis/linalg"
	"github.com/unixpickle/num-analysis/linalg/cholesky"
	"github.com/unixpickle/num-analysis/linalg/ludecomp"
)

const DefaultBlockSize = 16

// A Compressor compresses and decompresses images by changing
// each block of an image into a different linear basis and
// then removing basis vectors that aren't used very heavily.
type Compressor struct {
	quality float64
	basis   *linalg.Matrix
	basisLU *ludecomp.LU

	blockSize int
}

// NewCompressorBasis creates a Compressor that uses a custom
// block size, quality, and basis.
//
// The quality argument ranges from 0 to 1 and indicates the
// fraction of the basis elements that should be used in the
// compressed image without being pruned.
//
// Since the Compressor works on blocks of an image at a time,
// the blockSize specifies the size of the blocks it works on.
// Each blockSize-by-blockSize chunk of each input image will
// be compressed separately.
//
// The basis matrix is a matrix of column vectors.
// A basis will work best if its columns are normalized,
// but it needn't be orthonormal.
func NewCompressorBasis(quality float64, blockSize int, basis *linalg.Matrix) *Compressor {
	if !basis.Square() {
		panic("basis must be square")
	}
	return &Compressor{
		quality:   quality,
		basis:     basis,
		basisLU:   ludecomp.Decompose(basis),
		blockSize: blockSize,
	}
}

// NewCompressorBlockSize is like NewCompressionBasis, but it
// uses a basis generated by BasisMatrix.
func NewCompressorBlockSize(quality float64, blockSize int) *Compressor {
	basis := BasisMatrix(blockSize * blockSize)
	return NewCompressorBasis(quality, blockSize, basis)
}

// NewCompressor is like NewCompressorBlockSize, but uses
// DefaultBlockSize.
func NewCompressor(quality float64) *Compressor {
	return NewCompressorBlockSize(quality, DefaultBlockSize)
}

// Compress compresses an image and returns binary data
// representing the result.
func (c *Compressor) Compress(i image.Image) []byte {
	blocks := c.blocksInImage(i)
	r := &RankedVectors{
		BasisIndices: make([]int, c.blockSize*c.blockSize),
		CoeffTotal:   make([]float64, c.blockSize*c.blockSize),
	}
	for i := range r.BasisIndices {
		r.BasisIndices[i] = i
	}
	for _, block := range blocks {
		solution := c.basisLU.Solve(block)
		for i, coeff := range solution {
			r.CoeffTotal[i] += math.Abs(coeff)
		}
	}

	sort.Sort(r)
	basisCount := roundFloat(c.quality * float64(c.blockSize*c.blockSize))
	usedBasis := make([]int, basisCount)
	copy(usedBasis, r.BasisIndices)
	sort.Ints(usedBasis)

	basisVectors := c.basisVectors(usedBasis)

	projBlocks := c.projectionBlocks(basisVectors, blocks)

	compressed := &compressedImage{
		UsedBasis: usedBasis,
		Blocks:    projBlocks,
		BlockSize: c.blockSize,
		Width:     i.Bounds().Dx(),
		Height:    i.Bounds().Dy(),
	}
	return compressed.Encode()
}

// Decompress decodes the binary data of a compressed image,
// turning it back into a usable image.
func (c *Compressor) Decompress(d []byte) (image.Image, error) {
	ci, err := decodeCompressedImage(d, c.blockSize)
	if err != nil {
		return nil, err
	}

	// decodeCompressedImage does not verify the basis list.
	// We must verify the basis to prevent a possible panic().
	if !sort.IntsAreSorted(ci.UsedBasis) {
		return nil, errors.New("unsorted basis vectors in decoded image")
	}
	for _, x := range ci.UsedBasis {
		if x >= c.basis.Rows || x < 0 {
			return nil, errors.New("overflowing basis vectors in decoded image")
		}
	}

	basisVectors := c.basisVectors(ci.UsedBasis)

	blocks := make([][]float64, len(ci.Blocks))
	for i, encodedBlock := range ci.Blocks {
		if len(basisVectors) > 0 {
			blocks[i] = linearCombination(basisVectors, encodedBlock)
		} else {
			blocks[i] = make([]float64, c.blockSize*c.blockSize)
		}
	}

	return c.blocksToImage(ci.Width, ci.Height, blocks), nil
}

func (c *Compressor) basisVectors(indices []int) []linalg.Vector {
	basisVectors := make([]linalg.Vector, len(indices))
	for i, x := range indices {
		vec := make(linalg.Vector, c.blockSize*c.blockSize)
		for j := range vec {
			vec[j] = c.basis.Get(j, x)
		}
		basisVectors[i] = vec
	}
	return basisVectors
}

// projectionBlocks projects the blocks onto a pruned basis.
// The returned blocks hold the coefficients for the linear
// combination of basis elements that get as close to each
// original block as possible (i.e. that arrive at an
// orthogonal projection).
func (c *Compressor) projectionBlocks(basis, blocks []linalg.Vector) [][]float64 {
	// If we have an equation Ax=b where A is the matrix with
	// our pruned basis for columns, then we would like to find
	// the x which minimizes the magnitude ||Ax-b||. To do this,
	// we multiply on the left by the transpose of A, giving
	// (A^T)Ax = (A^T)b.

	// projLeft corresponds to (A^T)A in the above explanation.
	projLeft := linalg.NewMatrix(len(basis), len(basis))
	for i, v := range basis {
		for j, u := range basis {
			dot := v.Dot(u)
			projLeft.Set(i, j, dot)
		}
	}

	projLeftLU := cholesky.Decompose(projLeft)

	res := make([][]float64, len(blocks))
	for i, block := range blocks {
		// blockDot corresponds to (A^T)b in the explanation above.
		blockDot := make(linalg.Vector, len(basis))
		for k := range blockDot {
			blockDot[k] = basis[k].Dot(block)
		}
		solution := projLeftLU.Solve(blockDot)
		res[i] = []float64(solution)
	}

	return res
}

func (c *Compressor) blocksInImage(i image.Image) []linalg.Vector {
	numRows, numCols := c.blockCounts(i.Bounds())

	res := make([]linalg.Vector, 0, 3*numRows*numCols)
	for row := 0; row < numRows; row++ {
		for col := 0; col < numCols; col++ {
			startX := i.Bounds().Min.X + col*c.blockSize
			startY := i.Bounds().Min.Y + row*c.blockSize
			blocks := make([]linalg.Vector, 3)
			for i := range blocks {
				blocks[i] = make(linalg.Vector, c.blockSize*c.blockSize)
			}
			for y := 0; y < c.blockSize; y++ {
				if y+startY >= i.Bounds().Max.Y {
					continue
				}
				for x := 0; x < c.blockSize; x++ {
					if x+startX >= i.Bounds().Max.X {
						continue
					}
					px := i.At(x+startX, y+startY)
					r, g, b, _ := px.RGBA()
					idx := y * c.blockSize
					if y%2 == 0 {
						idx += x
					} else {
						idx += c.blockSize - (x + 1)
					}
					blocks[0][idx] = float64(r) / 0xffff
					blocks[1][idx] = float64(g) / 0xffff
					blocks[2][idx] = float64(b) / 0xffff
				}
			}
			res = append(res, blocks...)
		}
	}

	return res
}

func (c *Compressor) blocksToImage(w, h int, blocks [][]float64) image.Image {
	res := image.NewRGBA(image.Rect(0, 0, w, h))
	rows, cols := c.blockCounts(res.Bounds())

	blockIdx := 0
	for row := 0; row < rows; row++ {
		for col := 0; col < cols; col++ {
			colorBlocks := blocks[blockIdx : blockIdx+3]
			blockIdx += 3
			for y := 0; y < c.blockSize; y++ {
				if y+row*c.blockSize >= h {
					continue
				}
				for x := 0; x < c.blockSize; x++ {
					if x+col*c.blockSize >= w {
						continue
					}
					pxIdx := y * c.blockSize
					if y%2 == 0 {
						pxIdx += x
					} else {
						pxIdx += c.blockSize - (x + 1)
					}
					rVal := math.Min(math.Max(colorBlocks[0][pxIdx], 0), 1)
					gVal := math.Min(math.Max(colorBlocks[1][pxIdx], 0), 1)
					bVal := math.Min(math.Max(colorBlocks[2][pxIdx], 0), 1)
					px := color.RGBA{
						R: uint8(rVal * 0xff),
						G: uint8(gVal * 0xff),
						B: uint8(bVal * 0xff),
						A: 0xff,
					}
					res.Set(x+col*c.blockSize, y+row*c.blockSize, px)
				}
			}
		}
	}

	return res
}

func (c *Compressor) blockCounts(bounds image.Rectangle) (rows, cols int) {
	cols = bounds.Dx() / c.blockSize
	if bounds.Dx()%c.blockSize != 0 {
		cols++
	}

	rows = bounds.Dy() / c.blockSize
	if bounds.Dy()%c.blockSize != 0 {
		rows++
	}

	return
}

type RankedVectors struct {
	BasisIndices []int
	CoeffTotal   []float64
}

func (r *RankedVectors) Len() int {
	return len(r.BasisIndices)
}

func (r *RankedVectors) Less(i, j int) bool {
	return r.CoeffTotal[i] > r.CoeffTotal[j]
}

func (r *RankedVectors) Swap(i, j int) {
	r.CoeffTotal[i], r.CoeffTotal[j] = r.CoeffTotal[j], r.CoeffTotal[i]
	r.BasisIndices[i], r.BasisIndices[j] = r.BasisIndices[j], r.BasisIndices[i]
}
