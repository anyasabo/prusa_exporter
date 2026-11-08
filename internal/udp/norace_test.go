//go:build !race

package udp

// raceEnabled is true in a build with -race. The race detector adds allocations.
const raceEnabled = false
