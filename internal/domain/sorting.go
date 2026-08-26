package domain

import "sort"

// SortedDeviceIDs returns a deterministic copy of device identifiers.
func SortedDeviceIDs(ids []string) []string {
	out := append([]string(nil), ids...)
	sort.Strings(out)
	return out
}
