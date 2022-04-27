package utils

import "hash/fnv"

// Hash calculates has of the given string
func Hash(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}
