package main

import "math/rand"

// withTestRNGSeed temporarily overrides the global RNG to a deterministic seed for tests.
func withTestRNGSeed(seed int64, fn func()) {
	old := rng
	rng = rand.New(rand.NewSource(seed))
	defer func() { rng = old }()
	fn()
}
