//go:build !cgo
// +build !cgo

package metrics

// Note: go sigar is written in pure cgo, we cannot retrieve CPU stats without cgo.
// ReadCPUStats retrieves the current CPU stats.
func ReadCPUStats(stats *CPUStats) {
}
