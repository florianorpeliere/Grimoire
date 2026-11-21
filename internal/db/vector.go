package db

import "strconv"

// VectorLiteral formats v as pgvector's text input, e.g. [0.1,-2,3e-05], to
// be passed as a parameter cast to vector ($1::vector). Writes and searches
// share it so that stored and queried vectors are formatted the same way.
func VectorLiteral(v []float32) string {
	b := make([]byte, 0, len(v)*12)
	b = append(b, '[')
	for i, f := range v {
		if i > 0 {
			b = append(b, ',')
		}
		b = strconv.AppendFloat(b, float64(f), 'g', -1, 32)
	}
	return string(append(b, ']'))
}
