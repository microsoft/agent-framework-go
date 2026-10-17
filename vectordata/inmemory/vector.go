// Copyright (c) Microsoft. All rights reserved.

package inmemory

import (
	"fmt"
	"math"

	"github.com/microsoft/agent-framework-go/vectordata"
)

func validateVector(vector []float32, dimensions int) error {
	if len(vector) != dimensions {
		return fmt.Errorf("expected vector dimensions %d, got %d", dimensions, len(vector))
	}
	return nil
}

func sortDescending(distance vectordata.DistanceFunction) (bool, error) {
	switch distance {
	case "", vectordata.CosineSimilarity, vectordata.DotProductSimilarity:
		return true, nil
	case vectordata.CosineDistance, vectordata.EuclideanDistance:
		return false, nil
	default:
		return false, fmt.Errorf("distance function %q is not supported", distance)
	}
}

func compareVectors(x, y []float32, distance vectordata.DistanceFunction) float32 {
	var dot, xSquared, ySquared, squaredDistance float64
	for i, xv := range x {
		a, b := float64(xv), float64(y[i])
		dot += a * b
		xSquared += a * a
		ySquared += b * b
		delta := a - b
		squaredDistance += delta * delta
	}
	switch distance {
	case vectordata.DotProductSimilarity:
		return float32(dot)
	case vectordata.EuclideanDistance:
		return float32(math.Sqrt(squaredDistance))
	default:
		similarity := float32(dot / (math.Sqrt(xSquared) * math.Sqrt(ySquared)))
		if distance == vectordata.CosineDistance {
			return 1 - similarity
		}
		return similarity
	}
}
