package domain

import (
	"strconv"
	"strings"
)

// CompareVersions compares the numeric core and dot-separated prerelease
// identifiers used by EzhikLB. It returns -1, 0 or 1.
func CompareVersions(left, right string) int {
	parse := func(value string) ([]int, []string) {
		parts := strings.SplitN(strings.TrimPrefix(strings.TrimSpace(value), "v"), "-", 2)
		coreParts := strings.Split(parts[0], ".")
		core := make([]int, 3)
		for index := range core {
			if index < len(coreParts) {
				core[index], _ = strconv.Atoi(coreParts[index])
			}
		}
		if len(parts) == 1 {
			return core, nil
		}
		return core, strings.Split(parts[1], ".")
	}
	lCore, lPre := parse(left)
	rCore, rPre := parse(right)
	for index := 0; index < 3; index++ {
		if lCore[index] < rCore[index] {
			return -1
		}
		if lCore[index] > rCore[index] {
			return 1
		}
	}
	if len(lPre) == 0 && len(rPre) > 0 {
		return 1
	}
	if len(rPre) == 0 && len(lPre) > 0 {
		return -1
	}
	depth := len(lPre)
	if len(rPre) > depth {
		depth = len(rPre)
	}
	for index := 0; index < depth; index++ {
		if index >= len(lPre) {
			return -1
		}
		if index >= len(rPre) {
			return 1
		}
		li, lErr := strconv.Atoi(lPre[index])
		ri, rErr := strconv.Atoi(rPre[index])
		if lErr == nil && rErr == nil {
			if li < ri {
				return -1
			}
			if li > ri {
				return 1
			}
			continue
		}
		if lErr == nil {
			return -1
		}
		if rErr == nil {
			return 1
		}
		if lPre[index] < rPre[index] {
			return -1
		}
		if lPre[index] > rPre[index] {
			return 1
		}
	}
	return 0
}
