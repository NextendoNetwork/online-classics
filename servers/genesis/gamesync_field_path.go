package main

import "strings"

// Genesis NPLN 1.27.0 serializes a single field name as `name` (main
// 0x275630, called by the transform builder at 0x2e1ddc). Those backticks
// delimit a path component; they are not part of the document's map key.
// Only the flat, unescaped form observed in this client is supported here.
func gsFlatFieldPath(path string) (string, *gsError) {
	if strings.HasPrefix(path, "`") && strings.HasSuffix(path, "`") && len(path) >= 2 {
		path = path[1 : len(path)-1]
	}
	if path == "" || len(path) > gsMaxSegment || strings.ContainsAny(path, ".`\\*/") {
		return "", gsFail("12", "Nested or escaped field path not observed")
	}
	return path, nil
}
