package index

import (
	"encoding/binary"
	"math"
	"time"
)

type scanner interface {
	Scan(dest ...any) error
}

// encodeEmbedding serializes float32 slice to little-endian bytes.
func encodeEmbedding(v []float32) []byte {
	if len(v) == 0 {
		return nil
	}
	b := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(f))
	}
	return b
}

func decodeEmbedding(b []byte) []float32 {
	if len(b) == 0 {
		return nil
	}
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

func cosine(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}

// decayImportance returns effective importance after linear time decay.
// Decays from base to base*0.6 over 180 days, minimum base*0.6.
func decayImportance(base float64, updatedAt time.Time) float64 {
	days := time.Since(updatedAt).Hours() / 24
	decay := 1.0 - (days/180.0)*0.4
	if decay < 0.6 {
		decay = 0.6
	}
	return base * decay
}
